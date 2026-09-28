package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/format"
)

// pickMulti is the picker seam (the real one needs a terminal).
var pickMulti = PickMulti

// Prompter asks through the terminal. One shared reader serves every
// prompt, so consecutive answers on piped input are not lost.
type Prompter struct {
	in          *bufio.Reader
	out         *StdWriter
	interactive bool
}

var _ app.Prompter = (*Prompter)(nil)

// NewPrompter reads answers from in and writes prompts to out. When not
// interactive, Pick returns every item without showing the picker.
func NewPrompter(in io.Reader, out *StdWriter, interactive bool) *Prompter {
	return &Prompter{in: bufio.NewReader(in), out: out, interactive: interactive}
}

// Pick shows the multi-select picker, all items preselected.
func (p *Prompter) Pick(items []domain.Item) ([]domain.Item, error) {
	if !p.interactive || len(items) == 0 {
		return items, nil
	}
	choices := make([]PickerItem, len(items))
	for i, item := range items {
		choices[i] = PickerItem{ID: item.Code, Label: pickerLabel(item), Detail: pickerDetail(item), Selected: true}
	}
	picked, err := pickMulti("Select videos to download", choices)
	if errors.Is(err, ErrPickerCancelled) {
		return nil, app.ErrCancelled
	}
	if err != nil {
		return nil, fmt.Errorf("video picker: %w", err)
	}
	selected := make([]domain.Item, 0, len(items))
	for i, choice := range picked {
		if choice.Selected && i < len(items) {
			selected = append(selected, items[i])
		}
	}
	return selected, nil
}

// Confirm asks a [Y/n] question; an empty answer means yes, end of input
// means no.
func (p *Prompter) Confirm(prompt string) (bool, error) {
	p.out.Printf("\n  %s%s [Y/n]%s ", ColorBold, prompt, ColorReset)
	line, err := p.in.ReadString('\n')
	if errors.Is(err, io.EOF) && line == "" {
		return false, nil
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func pickerLabel(item domain.Item) string {
	if item.Title == "" {
		return item.Code
	}
	return item.Code + " · " + item.Title
}

func pickerDetail(item domain.Item) string {
	detail := item.Duration
	if detail == "" {
		detail = "duration unknown"
	}
	if estimate := app.EstimateVideoBytes(item.Duration); estimate > 0 {
		detail += " · ~" + format.Bytes(estimate)
	}
	if item.Site != "" {
		detail = item.Site + " · " + detail
	}
	return detail
}
