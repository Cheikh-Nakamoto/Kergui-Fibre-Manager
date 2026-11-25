package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// MaxLabels bounds one exchange of labels: a household has a few dozen
// devices, a few hundred over the years; this leaves room without letting a
// command fill the database.
const MaxLabels = 2000

// MaxLabelText bounds a name or notes.
const MaxLabelText = 500

// ErrBadLabels is a set of labels that cannot be applied as a whole.
var ErrBadLabels = fmt.Errorf("labels: %w", domain.ErrInvalidInput)

// Labels are the household's names and notes of its devices, with when they
// were set (#95). The owner's phone and the household's relay each keep a
// copy in their own database; they exchange them — the phone through
// commands the owner signs, the relay through the device list it signs — and
// the later edit wins. Purely local: it never touches the router.
type Labels struct {
	repo port.LabelRepository
}

// NewLabels wires the interactor.
func NewLabels(repo port.LabelRepository) *Labels { return &Labels{repo: repo} }

// List returns the labels kept for the router at baseURL.
func (uc *Labels) List(ctx context.Context, baseURL string) ([]domain.Label, error) {
	return uc.repo.Labels(ctx, RouterID(baseURL))
}

// Apply applies labels made elsewhere, last write wins, and returns the MACs
// whose label changed. A label without a MAC or a time is refused with the
// whole set: it would be one nobody can order.
func (uc *Labels) Apply(ctx context.Context, baseURL string, labels []domain.Label) ([]domain.MAC, error) {
	if err := ValidateLabels(labels); err != nil {
		return nil, err
	}
	return uc.repo.ApplyLabels(ctx, RouterID(baseURL), labels)
}

// ValidateLabels checks a set of labels before it is applied or signed.
func ValidateLabels(labels []domain.Label) error {
	if len(labels) > MaxLabels {
		return fmt.Errorf("%w: more than %d labels", ErrBadLabels, MaxLabels)
	}
	for _, l := range labels {
		switch {
		case l.MAC.IsZero():
			return fmt.Errorf("%w: a label has no MAC", ErrBadLabels)
		case l.At.IsZero():
			return fmt.Errorf("%w: the label of %s has no time", ErrBadLabels, l.MAC)
		case l.Name != nil && len(*l.Name) > MaxLabelText, l.Notes != nil && len(*l.Notes) > MaxLabelText:
			return fmt.Errorf("%w: the label of %s is too long", ErrBadLabels, l.MAC)
		}
	}
	return nil
}
