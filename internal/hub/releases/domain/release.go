// Package domain holds the rules of the app's releases (issue #65): what a
// declared build must carry, the states it goes through, which build a
// phone is offered, and the minimum build a phone must run.
//
// A release is a build of the app on one platform and channel. CI declares
// it (pending); a platform administrator publishes it after trying it, and
// may withdraw it. The newest published build above a phone's own is the
// one offered. The minimum is a build number per platform and channel:
// below it the app blocks until it is updated, so it must always stand on a
// published build.
package domain

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Platform is the operating system a build runs on.
type Platform string

// Android is the only platform with self-updates: iOS has no sideloading.
const Android Platform = "android"

// Channel is how a build reaches phones.
type Channel string

// Website is the APK downloaded from kergui.click. The Play build gets
// Google's own updates and is never declared here.
const Website Channel = "website"

// State is where a release stands.
type State string

// The states of a release.
const (
	Pending   State = "pending"
	Published State = "published"
	Withdrawn State = "withdrawn"
)

// Limits of what a declaration and a check may carry.
const (
	// MaxBuild is Android's largest versionCode.
	MaxBuild          = 2_100_000_000
	MaxVersionNameLen = 64
	MaxURLLen         = 2048
	MaxNotesLen       = 4000
	// MaxSize is far above any APK; it only keeps a typo out.
	MaxSize = 1 << 30
	// The anonymous installation id: random, generated once by the app.
	MinInstallationIDLen = 8
	MaxInstallationIDLen = 64
)

// CountWindow is how far back an installation counts towards the phones
// running a version.
const CountWindow = 30 * 24 * time.Hour

// Errors. ErrInvalid wraps the reason; the others are refusals of a rule.
var (
	ErrInvalid = errors.New("releases: invalid")
	// ErrState is a transition the release's state does not allow.
	ErrState = errors.New("releases: not allowed in this state")
	// ErrNotPublished refuses a minimum on a build nobody can download.
	ErrNotPublished = errors.New("releases: the minimum must be a published build")
	// ErrMinimumUncovered refuses a withdrawal that would leave phones
	// below the minimum with nothing to update to.
	ErrMinimumUncovered = errors.New("releases: the minimum would have no published build left")
	// ErrFileChanged refuses a declaration that changes the file of a build
	// already published or withdrawn.
	ErrFileChanged = errors.New("releases: this build was already declared with another file")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Target is one platform and channel: the unit releases, the minimum and
// the counts are kept per.
type Target struct {
	Platform Platform
	Channel  Channel
}

// Validate refuses a platform or channel the hub does not update.
func (t Target) Validate() error {
	if t.Platform != Android {
		return invalid("unknown platform %q", t.Platform)
	}
	if t.Channel != Website {
		return invalid("unknown channel %q", t.Channel)
	}
	return nil
}

// ValidateBuild checks a build number: Android's versionCode, 1 and up.
func ValidateBuild(b int64) error {
	if b < 1 || b > MaxBuild {
		return invalid("the build number must be between 1 and %d", MaxBuild)
	}
	return nil
}

var installationID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidateInstallationID checks the app's anonymous id: letters, digits,
// '-' and '_', of a bounded length, so nothing else can be smuggled in.
func ValidateInstallationID(id string) error {
	if len(id) < MinInstallationIDLen || len(id) > MaxInstallationIDLen || !installationID.MatchString(id) {
		return invalid("the installation id must be %d to %d letters, digits, '-' or '_'", MinInstallationIDLen, MaxInstallationIDLen)
	}
	return nil
}

// ValidateNotes checks release notes.
func ValidateNotes(notes string) error {
	if !utf8.ValidString(notes) || utf8.RuneCountInString(notes) > MaxNotesLen {
		return invalid("the notes must be valid text of at most %d characters", MaxNotesLen)
	}
	return nil
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Declaration is what CI declares about a build.
type Declaration struct {
	Target
	VersionName string
	Build       int64
	URL         string
	SHA256      string
	Size        int64
	Notes       string
}

// Normalize trims the text fields and lowercases the digest.
func (d Declaration) Normalize() Declaration {
	d.VersionName = strings.TrimSpace(d.VersionName)
	d.URL = strings.TrimSpace(d.URL)
	d.SHA256 = strings.ToLower(strings.TrimSpace(d.SHA256))
	d.Notes = strings.TrimSpace(d.Notes)
	return d
}

// Validate checks a normalized declaration.
func (d Declaration) Validate() error {
	if err := d.Target.Validate(); err != nil {
		return err
	}
	if err := ValidateBuild(d.Build); err != nil {
		return err
	}
	if d.VersionName == "" || len(d.VersionName) > MaxVersionNameLen || !utf8.ValidString(d.VersionName) {
		return invalid("the version name is required (at most %d characters)", MaxVersionNameLen)
	}
	if len(d.URL) > MaxURLLen {
		return invalid("the URL is too long")
	}
	// The app downloads it on the household's data: HTTPS only.
	if u, err := url.Parse(d.URL); err != nil || u.Scheme != "https" || u.Host == "" {
		return invalid("the URL must be an https:// link")
	}
	if !sha256Hex.MatchString(d.SHA256) {
		return invalid("the SHA-256 must be 64 hexadecimal characters")
	}
	if d.Size < 1 || d.Size > MaxSize {
		return invalid("the size must be between 1 and %d bytes", MaxSize)
	}
	return ValidateNotes(d.Notes)
}

// Release is a declared build.
type Release struct {
	ID string
	Declaration
	State       State
	DeclaredAt  time.Time
	PublishedAt *time.Time
	WithdrawnAt *time.Time
}

// SameFile reports whether a declaration describes this release's file.
func (r Release) SameFile(d Declaration) bool {
	return r.VersionName == d.VersionName && r.URL == d.URL && r.SHA256 == d.SHA256 && r.Size == d.Size
}

// Redeclare is a declaration of a build already known. Declaring the same
// file again changes nothing: a re-run release job is harmless. A pending
// build takes the new file (a rebuilt APK has a new digest), keeping notes
// already written unless it had none. A published or withdrawn build keeps
// its file: phones may already have downloaded and checked it.
func (r Release) Redeclare(d Declaration) (Release, bool, error) {
	if r.SameFile(d) {
		return r, false, nil
	}
	if r.State != Pending {
		return r, false, ErrFileChanged
	}
	notes := r.Notes
	if notes == "" {
		notes = d.Notes
	}
	r.Declaration = d
	r.Notes = notes
	return r, true, nil
}

// Publish offers the release to phones. A withdrawn build may be published
// again, to undo a withdrawal made by mistake.
func (r Release) Publish(now time.Time) (Release, error) {
	if r.State != Pending && r.State != Withdrawn {
		return r, ErrState
	}
	r.State, r.PublishedAt, r.WithdrawnAt = Published, &now, nil
	return r, nil
}

// Withdraw stops offering the release.
func (r Release) Withdraw(now time.Time) (Release, error) {
	if r.State != Published {
		return r, ErrState
	}
	r.State, r.WithdrawnAt = Withdrawn, &now
	return r, nil
}

// Offer is the newest published release above build, or nil.
func Offer(releases []Release, build int64) *Release {
	var best *Release
	for i := range releases {
		r := &releases[i]
		if r.State == Published && r.Build > build && (best == nil || r.Build > best.Build) {
			best = r
		}
	}
	return best
}

// Covered reports whether a published release at or above the minimum
// remains once the release with id except is set aside. No minimum (0) is
// always covered.
func Covered(releases []Release, minimum int64, except string) bool {
	if minimum <= 0 {
		return true
	}
	for _, r := range releases {
		if r.ID != except && r.State == Published && r.Build >= minimum {
			return true
		}
	}
	return false
}

// CheckMinimum refuses a minimum that is not a published build. 0 clears it.
func CheckMinimum(releases []Release, build int64) error {
	if build == 0 {
		return nil
	}
	if err := ValidateBuild(build); err != nil {
		return err
	}
	for _, r := range releases {
		if r.Build == build && r.State == Published {
			return nil
		}
	}
	return ErrNotPublished
}
