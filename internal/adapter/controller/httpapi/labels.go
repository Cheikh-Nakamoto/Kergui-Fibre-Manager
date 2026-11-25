package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// The household's labels of its devices — names and notes, with when they
// were set (#95). GET /api/labels lists them; POST /api/labels applies labels
// made elsewhere, last write wins. The owner's phone reads its own to send
// them to the relay, and the relay runs POST /api/labels when the owner (or
// a member with control) signs it: so the names are the same whether the
// app talks to the box or goes through the relay.

// LabelJSON is one label on the wire. A missing name or notes leaves that
// field as it is; at is RFC 3339, millisecond precision is what is kept.
type LabelJSON struct {
	MAC   string    `json:"mac"`
	Name  *string   `json:"name,omitempty"`
	Notes *string   `json:"notes,omitempty"`
	At    time.Time `json:"at"`
}

// LabelsJSON is the body of POST /api/labels and the answer of GET.
type LabelsJSON struct {
	Labels []LabelJSON `json:"labels"`
}

// DecodeLabels reads a POST /api/labels body strictly: an unknown field, a
// MAC that does not parse or a label without a time refuses the whole set.
// The phone's core runs it before signing, the API before applying.
func DecodeLabels(body []byte) ([]domain.Label, error) {
	var req LabelsJSON
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("%w: %v", usecase.ErrBadLabels, err)
	}
	out := make([]domain.Label, 0, len(req.Labels))
	for _, l := range req.Labels {
		m, err := domain.ParseMAC(strings.TrimSpace(l.MAC))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", usecase.ErrBadLabels, err)
		}
		var name, notes *string
		if l.Name != nil {
			n := strings.TrimSpace(*l.Name)
			name = &n
		}
		if l.Notes != nil {
			n := strings.TrimSpace(*l.Notes)
			notes = &n
		}
		out = append(out, domain.Label{MAC: m, Name: name, Notes: notes, At: l.At.UTC()})
	}
	if err := usecase.ValidateLabels(out); err != nil {
		return nil, err
	}
	return out, nil
}

func labelJSON(l domain.Label) LabelJSON {
	return LabelJSON{MAC: strings.ToLower(l.MAC.String()), Name: l.Name, Notes: l.Notes, At: l.At}
}

func (s *Server) labels(w http.ResponseWriter, r *http.Request) {
	if s.svc.Labels == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "labels are not kept here"})
		return
	}
	list, err := s.svc.Labels.List(r.Context(), s.cfg.BaseURL)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	out := LabelsJSON{Labels: make([]LabelJSON, 0, len(list))}
	for _, l := range list {
		out.Labels = append(out.Labels, labelJSON(l))
	}
	writeJSON(w, http.StatusOK, out)
}

// applyLabels answers {"changed": [MACs]}. A device given a name is one the
// household knows: accepted under auto-reject, as a rename here is.
func (s *Server) applyLabels(w http.ResponseWriter, r *http.Request) {
	if s.svc.Labels == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "labels are not kept here"})
		return
	}
	body, err := readLimited(w, r, 1<<20)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	labels, err := DecodeLabels(body)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	changed, err := s.svc.Labels.Apply(r.Context(), s.cfg.BaseURL, labels)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	named := map[domain.MAC]bool{}
	for _, l := range labels {
		if l.Name != nil && *l.Name != "" {
			named[l.MAC] = true
		}
	}
	out := make([]string, 0, len(changed))
	for _, m := range changed {
		out = append(out, strings.ToLower(m.String()))
		if named[m] {
			s.autoAccept(r, m)
		}
	}
	if len(changed) > 0 {
		s.svc.Logger.Infof("noms : %d appareil(s) mis à jour", len(changed))
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": out})
}

func readLimited(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(http.MaxBytesReader(w, r.Body, limit))
	return buf.Bytes(), err
}
