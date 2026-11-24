package usecase_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/usecase"
)

type version struct {
	b          domain.Backup
	replacedAt time.Time
}

type memStore struct {
	current  map[[2]string]domain.Backup
	versions []version
}

func newMem() *memStore { return &memStore{current: map[[2]string]domain.Backup{}} }

func (m *memStore) InBox(ctx context.Context, _, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

func (m *memStore) Current(_ context.Context, t, b string) (domain.Backup, error) {
	cur, ok := m.current[[2]string{t, b}]
	if !ok {
		return domain.Backup{}, domain.ErrNotFound
	}
	return cur, nil
}

func (m *memStore) List(_ context.Context, t string) ([]domain.Backup, error) {
	var out []domain.Backup
	for k, b := range m.current {
		if k[0] == t {
			b.Archive = nil
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *memStore) Put(_ context.Context, b domain.Backup) error {
	m.current[[2]string{b.TenantID, b.BoxID}] = b
	return nil
}

func (m *memStore) KeepVersion(_ context.Context, b domain.Backup, at time.Time) error {
	m.versions = append(m.versions, version{b, at})
	return nil
}

func (m *memStore) PruneVersions(_ context.Context, t, box string, keep int, since time.Time) error {
	sort.SliceStable(m.versions, func(i, j int) bool { return m.versions[i].replacedAt.After(m.versions[j].replacedAt) })
	var out []version
	n := 0
	for _, v := range m.versions {
		if v.b.TenantID == t && v.b.BoxID == box {
			if n >= keep || v.replacedAt.Before(since) {
				continue
			}
			n++
		}
		out = append(out, v)
	}
	m.versions = out
	return nil
}

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func archive(t *testing.T, devices []backup.Device, usage []backup.Usage) []byte {
	t.Helper()
	b, err := backup.Archive{Devices: devices, Usage: usage}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const tenant, box = "11111111-1111-1111-1111-111111111111", "192-168-1-1"

func TestSaveMergesASecondSourceAndKeepsTheReplacedVersion(t *testing.T) {
	store, clk := newMem(), &clock{time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)}
	svc := &usecase.Service{Store: store, Now: clk.Now}
	ctx := context.Background()
	phone := domain.Source{Kind: domain.SourcePhone, ID: "user-1"}
	pc := domain.Source{Kind: domain.SourceRelay, ID: "agent-1"}

	first, err := svc.Save(ctx, tenant, box, phone, archive(t,
		[]backup.Device{{MAC: "aa:bb:cc:00:00:01", Name: "Télé"}, {MAC: "aa:bb:cc:00:00:02", Name: "Tablette"}},
		[]backup.Usage{{MAC: "aa:bb:cc:00:00:01", Month: "2026-11", Bytes: 10}}))
	if err != nil {
		t.Fatal(err)
	}
	if first.Merged || first.Devices != 2 || first.Months != 1 || first.Source != phone || !first.UpdatedAt.Equal(clk.t) {
		t.Fatalf("first save = %+v", first)
	}
	if len(store.versions) != 0 {
		t.Fatalf("a first backup replaced something: %d versions", len(store.versions))
	}

	clk.t = clk.t.Add(time.Hour)
	second, err := svc.Save(ctx, tenant, box, pc, archive(t,
		[]backup.Device{{MAC: "aa:bb:cc:00:00:01", Name: "Télé du salon"}, {MAC: "aa:bb:cc:00:00:03", Name: "PC"}},
		[]backup.Usage{{MAC: "aa:bb:cc:00:00:01", Month: "2026-11", Bytes: 5}, {MAC: "aa:bb:cc:00:00:03", Month: "2026-10", Bytes: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Merged || second.Devices != 3 || second.Months != 2 || second.Source != pc || !second.UpdatedAt.Equal(clk.t) {
		t.Fatalf("second save = %+v", second)
	}
	got, err := svc.Get(ctx, tenant, box)
	if err != nil {
		t.Fatal(err)
	}
	a, err := backup.Read(got.Archive)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, d := range a.Devices {
		names[d.MAC] = d.Name
	}
	if names["aa:bb:cc:00:00:01"] != "Télé du salon" || names["aa:bb:cc:00:00:02"] != "Tablette" || names["aa:bb:cc:00:00:03"] != "PC" {
		t.Errorf("merged names = %v", names)
	}
	for _, u := range a.Usage {
		if u.MAC == "aa:bb:cc:00:00:01" && u.Bytes != 10 {
			t.Errorf("the larger month was not kept: %+v", u)
		}
	}
	if len(store.versions) != 1 || store.versions[0].b.Source != phone {
		t.Fatalf("versions = %+v", store.versions)
	}
}

func TestSaveKeepsAFewRecentVersionsOnly(t *testing.T) {
	store, clk := newMem(), &clock{time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	svc := &usecase.Service{Store: store, Now: clk.Now}
	src := domain.Source{Kind: domain.SourcePhone, ID: "u"}
	body := archive(t, []backup.Device{{MAC: "aa:bb:cc:00:00:01"}}, nil)
	for i := 0; i < 9; i++ {
		if _, err := svc.Save(context.Background(), tenant, box, src, body); err != nil {
			t.Fatal(err)
		}
		clk.t = clk.t.Add(time.Hour)
	}
	if len(store.versions) != usecase.KeepVersions {
		t.Fatalf("%d versions kept, want %d", len(store.versions), usecase.KeepVersions)
	}
	// A week later, the next save drops every version older than the window.
	clk.t = clk.t.Add(usecase.KeepVersionFor + time.Hour)
	if _, err := svc.Save(context.Background(), tenant, box, src, body); err != nil {
		t.Fatal(err)
	}
	if len(store.versions) != 1 {
		t.Fatalf("%d versions kept after a week, want the one just replaced", len(store.versions))
	}
}

func TestSaveRefusesWhatIsNotABackup(t *testing.T) {
	svc := &usecase.Service{Store: newMem()}
	src := domain.Source{Kind: domain.SourcePhone, ID: "u"}
	ctx := context.Background()
	if _, err := svc.Save(ctx, tenant, box, src, []byte("not a zip")); !errors.Is(err, backup.ErrBadBackup) {
		t.Errorf("not a zip: err = %v", err)
	}
	for _, bad := range []string{"", "../etc", "Box", strings.Repeat("a", 129)} {
		if _, err := svc.Save(ctx, tenant, bad, src, archive(t, nil, nil)); !errors.Is(err, domain.ErrBadBox) {
			t.Errorf("box %q: err = %v", bad, err)
		}
	}
	if _, err := svc.Save(ctx, tenant, box, src, make([]byte, domain.MaxArchive+1)); !errors.Is(err, domain.ErrTooLarge) {
		t.Errorf("too large: err = %v", err)
	}
	if _, err := svc.Get(ctx, tenant, box); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get with none: err = %v", err)
	}
}
