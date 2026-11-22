package gui

import (
	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

type windowFilters struct {
	MatchMode string `json:"window_match_mode"`
	Title     string `json:"window_title"`
	Class     string `json:"window_class"`
	PID       int    `json:"window_pid"`
	Image     string `json:"window_image"`
}

func windowFilterFields() []nodeapi.FieldSpec {
	return []nodeapi.FieldSpec{
		{ID: "window_match_mode", Label: "Window match mode", Type: flow.FieldSelect, Default: "regex", Options: []nodeapi.Option{{Value: "regex", Label: "Regular expression"}, {Value: "contains", Label: "Contains"}, {Value: "exact", Label: "Exact"}}},
		{ID: "window_title", Label: "Window title", Type: flow.FieldString, Help: "Additional title constraint using the selected match mode. Supports {{ }} expressions."},
		{ID: "window_class", Label: "Window class", Type: flow.FieldString, Help: "Additional class constraint using the selected match mode. Supports {{ }} expressions."},
		{ID: "window_pid", Label: "Window process id", Type: flow.FieldNumber, Help: "Zero accepts any process."},
		{ID: "window_image", Label: "Window executable", Type: flow.FieldString, Help: "Exact executable path or filename, when reported by the provider. Supports {{ }} expressions."},
	}
}

func (f windowFilters) target(ec nodeapi.ExecContext, msg *nodeapi.Msg, id, match string, active, all bool) (automation.WindowTarget, error) {
	for _, field := range []*string{&id, &match, &f.Title, &f.Class, &f.Image} {
		value, err := agentnode.Interpolate(ec, *field, msg)
		if err != nil {
			return automation.WindowTarget{}, err
		}
		*field = value
	}
	return automation.WindowTarget{ID: id, Match: match, Active: active, Regex: true, MatchMode: f.MatchMode, Title: f.Title, Class: f.Class, PID: f.PID, Image: f.Image, All: all}, nil
}

func hasWindowTarget(t automation.WindowTarget) bool {
	return t.ID != "" || t.Match != "" || t.Active || t.Title != "" || t.Class != "" || t.PID != 0 || t.Image != ""
}
