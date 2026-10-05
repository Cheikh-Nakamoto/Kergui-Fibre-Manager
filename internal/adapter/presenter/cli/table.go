// Package cli contains the CLI output presenters (output adapters). They render
// the use-case result data; the core never formats anything itself.
package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// TablePresenter renders human-friendly text.
type TablePresenter struct{ w io.Writer }

var _ port.Presenter = (*TablePresenter)(nil)

// NewTablePresenter writes to w.
func NewTablePresenter(w io.Writer) *TablePresenter { return &TablePresenter{w: w} }

func verifiedTag(ok bool) string {
	if ok {
		return "verified"
	}
	return "UNVERIFIED"
}

// Discovery renders the fingerprint result.
func (p *TablePresenter) Discovery(info domain.RouterInfo) error {
	fmt.Fprintln(p.w, "Router")
	fmt.Fprintln(p.w, "------")
	fmt.Fprintf(p.w, "URL:       %s\n", info.BaseURL)
	fmt.Fprintf(p.w, "Reachable: %v\n", info.Reachable)
	fmt.Fprintf(p.w, "Vendor:    %s\n", orDash(info.Vendor))
	fmt.Fprintf(p.w, "Model:     %s\n", orDash(info.Model))
	fmt.Fprintf(p.w, "Server:    %s\n", orDash(info.HTTPServer))
	fmt.Fprintf(p.w, "Title:     %s\n", orDash(info.Title))
	if info.AdapterID != "" {
		fmt.Fprintf(p.w, "Adapter:   %s (confidence %d%%)\n", info.AdapterID, info.Confidence)
	} else {
		fmt.Fprintf(p.w, "Adapter:   none matched — pass --adapter to choose one\n")
	}
	return nil
}

// Devices renders the inventory list with a summary header.
func (p *TablePresenter) Devices(views []port.DeviceView) error {
	var connected, blocked, newly int
	for _, v := range views {
		if v.Connected {
			connected++
		}
		if v.Blocked {
			blocked++
		}
		if v.NewlySeen {
			newly++
		}
	}
	fmt.Fprintln(p.w, "MON RÉSEAU")
	fmt.Fprintf(p.w, "%d appareils · %d connectés · %d bloqués · %d nouveaux\n\n", len(views), connected, blocked, newly)

	tw := tabwriter.NewWriter(p.w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tNAME\tIP\tMAC\tSSID\tFLAGS")
	for _, v := range views {
		status := "offline"
		if v.Connected {
			status = "online"
		}
		flags := ""
		if v.Blocked {
			flags += "BLOCKED "
		}
		if v.NewlySeen {
			flags += "NEW"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			status, v.DisplayName(), orDash(v.IP.String()), v.MAC, orDash(v.SSID), flags)
	}
	return tw.Flush()
}

// Report renders the inspect diagnostic in the brief's format.
func (p *TablePresenter) Report(rep port.InspectReport) error {
	endpoints := map[string]domain.EndpointDoc{}
	for _, e := range rep.Meta.Endpoints {
		endpoints[e.Operation] = e
	}
	section := func(title string) { fmt.Fprintf(p.w, "\n%s\n%s\n", title, dashes(len(title))) }

	section("Router")
	fmt.Fprintf(p.w, "URL:       %s\n", rep.Info.BaseURL)
	fmt.Fprintf(p.w, "Vendor:    %s\n", orDash(rep.Info.Vendor))
	fmt.Fprintf(p.w, "Model:     %s\n", orDash(rep.Info.Model))
	fmt.Fprintf(p.w, "Firmware:  %s\n", orDash(rep.Info.Firmware))
	fmt.Fprintf(p.w, "Server:    %s\n", orDash(rep.Info.HTTPServer))
	if rep.Meta.ID != "" {
		fmt.Fprintf(p.w, "Adapter:   %s [%s]\n", rep.Meta.ID, verifiedTag(rep.Meta.Verified))
	}

	section("Authentication")
	fmt.Fprintf(p.w, "Login:     %s\n", endpointLine(endpoints["login"]))
	fmt.Fprintf(p.w, "Auth OK:   %v\n", rep.AuthOK)

	section("Devices endpoint")
	fmt.Fprintf(p.w, "%s\n", endpointLine(endpoints["devices"]))
	fmt.Fprintf(p.w, "Count:     %d\n", rep.DeviceCount)

	section("Access control endpoint")
	fmt.Fprintf(p.w, "%s\n", endpointLine(endpoints["access_rules"]))
	fmt.Fprintf(p.w, "Count:     %d\n", rep.AccessRuleCount)

	section("Block / Unblock (documented — NOT executed in this milestone)")
	fmt.Fprintf(p.w, "Block:     %s\n", endpointLine(endpoints["block"]))
	fmt.Fprintf(p.w, "Unblock:   %s\n", endpointLine(endpoints["unblock"]))

	if len(rep.Warnings) > 0 {
		section("Warnings")
		for _, w := range rep.Warnings {
			fmt.Fprintf(p.w, "- %s\n", w)
		}
	}
	return nil
}

// Message renders a plain line.
func (p *TablePresenter) Message(msg string) error {
	_, err := fmt.Fprintln(p.w, msg)
	return err
}

func endpointLine(e domain.EndpointDoc) string {
	if e.Path == "" {
		return "(not documented)"
	}
	return fmt.Sprintf("%s %s [%s]", e.Method, e.Path, verifiedTag(e.Verified))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func dashes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}
