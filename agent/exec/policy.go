package exec

import (
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// processPolicy controls stream collection and accepted process outcomes.
type processPolicy struct {
	TerminationGrace  string `json:"termination_grace"`
	AcceptedExitCodes []int  `json:"accepted_exit_codes"`
	StdoutMode        string `json:"stdout_mode"`
	StderrMode        string `json:"stderr_mode"`
	ResultDestination string `json:"result_destination"`
	ResultProperty    string `json:"result_property"`
}

func processFields() []nodeapi.FieldSpec {
	fields := []nodeapi.FieldSpec{
		{ID: "termination_grace", Label: "Termination grace", Type: flow.FieldDuration, Default: "0s", Placeholder: "5s", Help: "Time for cleanup after cancellation or timeout, from 0s through 1m. Unix sends SIGTERM to the process group, then SIGKILL after grace. Zero forces termination immediately. Windows supports only zero. Cancellation and timeout still fail."},
		{ID: "accepted_exit_codes", Label: "Accepted exit codes", Type: flow.FieldJSON, Help: "Nonempty list of nonnegative integers. Unset accepts only zero; an explicit list takes precedence over Ignore the exit code."},
		{ID: "result_destination", Label: "Result destination", Type: flow.FieldSelect, Default: "payload", Options: []nodeapi.Option{{Value: "payload", Label: "Replace payload"}, {Value: "property", Label: "Store in a property"}}},
		{ID: "result_property", Label: "Result property", Type: flow.FieldString, Required: true, ShowIf: &nodeapi.ShowIf{Field: "result_destination", Equals: "property"}, Placeholder: "meta.command_result", Help: "Payload or metadata descendant. Other content is preserved; existing parents must be objects."},
	}
	for _, stream := range []string{"stdout", "stderr"} {
		fields = append(fields, nodeapi.FieldSpec{ID: stream + "_mode", Label: map[string]string{"stdout": "Standard output mode", "stderr": "Standard error mode"}[stream], Type: flow.FieldSelect, Default: "return", Options: []nodeapi.Option{{Value: "return", Label: "Return (optional file mirror)"}, {Value: "file", Label: "File only"}, {Value: "discard", Label: "Discard"}}, Help: "File only requires an output path and avoids the 32 MiB return limit. Discard cannot also write a file."})
	}
	return fields
}

func processRule() *flow.PortTypeRule {
	rule := agentnode.FormatRule()
	for i := range rule.Cases {
		rule.Cases[i].PayloadPath = []string{"result_property"}
		rule.Cases[i].Match["result_destination"] = []string{"property"}
	}
	propertyCases := rule.Cases
	rule.Cases = []flow.PortTypeCase{
		{Match: map[string][]string{"stdout_mode": {"file", "discard"}}, Type: ""},
	}
	rule.Cases = append(rule.Cases, propertyCases...)
	rule.Cases = append(rule.Cases, flow.PortTypeCase{Match: map[string][]string{"result_destination": {"property"}}, Type: flow.PortAny, PayloadPath: []string{"result_property"}})
	rule.Cases = append(rule.Cases, agentnode.FormatRule().Cases...)
	rule.Cases = append(rule.Cases, flow.PortTypeCase{Type: flow.PortAny})
	return rule
}

func (p processPolicy) validate(msg *nodeapi.Msg) error {
	if _, err := processTerminationGrace(p.TerminationGrace); err != nil {
		return err
	}
	if p.AcceptedExitCodes != nil {
		if len(p.AcceptedExitCodes) == 0 {
			return fmt.Errorf("accepted exit codes must not be empty")
		}
		for _, code := range p.AcceptedExitCodes {
			if code < 0 {
				return fmt.Errorf("accepted exit codes must be nonnegative")
			}
		}
	}
	for _, mode := range []string{p.StdoutMode, p.StderrMode} {
		if mode != "" && mode != "return" && mode != "file" && mode != "discard" {
			return fmt.Errorf("unknown stream mode %q", mode)
		}
	}
	switch p.ResultDestination {
	case "", "payload":
	case "property":
		_, err := agentnode.WithResult(msg, p.ResultProperty, nil)
		return err
	default:
		return fmt.Errorf("unknown result destination %q", p.ResultDestination)
	}
	return nil
}

func (p processPolicy) accepts(code int, ignore bool) bool {
	if code < 0 {
		return false
	}
	if p.AcceptedExitCodes == nil {
		return code == 0 || ignore
	}
	for _, accepted := range p.AcceptedExitCodes {
		if code == accepted {
			return true
		}
	}
	return false
}

func (p processPolicy) output(msg *nodeapi.Msg, format string, stdout []byte, meta map[string]any) (*nodeapi.Msg, error) {
	out := agentnode.PassThrough(msg, meta)
	if p.StdoutMode == "file" || p.StdoutMode == "discard" {
		return out, nil
	}
	payload, err := agentnode.DecodeOutput(format, stdout)
	if err != nil {
		return nil, err
	}
	if p.ResultDestination == "property" {
		return agentnode.WithResult(out, strings.TrimSpace(p.ResultProperty), payload)
	}
	out.Payload = payload
	return out, nil
}
