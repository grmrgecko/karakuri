// Package scriptverify selects the static checker for a script language and
// applies it to flow graphs without executing scripts or nodes.
package scriptverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
)

// ErrUnsupportedLanguage reports a source language with no static checker.
var ErrUnsupportedLanguage = errors.New("unsupported script language")

// CheckSource verifies statically inspectable calls in one source document. An
// empty language selects JavaScript, matching the script node's configuration.
func CheckSource(ctx context.Context, language, source string, pure bool, metas []flow.NodeMeta) ([]scriptsdk.Diagnostic, error) {
	switch normalizeLanguage(language) {
	case sandbox.LangJavaScript:
		return scriptsdk.CheckSource(source, pure, metas), nil
	case sandbox.LangPython:
		return sandbox.CheckPythonSource(ctx, source, pure, metas)
	case sandbox.LangBash:
		return scriptsdk.CheckBashSource(source, pure, metas), nil
	default:
		return nil, fmt.Errorf("%w %q; use javascript, python, or bash", ErrUnsupportedLanguage, language)
	}
}

// CheckGraph verifies script nodes in a graph and its local subgraphs. Keys use
// the same node locations as graph validation. A script that a value edge pulls,
// directly or through a reroute, a set/get pair, or a bypassed node, is checked
// as a pure producer.
func CheckGraph(ctx context.Context, graph *flow.Graph, metas []flow.NodeMeta) (map[string][]scriptsdk.Diagnostic, error) {
	findings := make(map[string][]scriptsdk.Diagnostic)
	if graph == nil {
		return findings, nil
	}
	var check func(*flow.Graph, string) error
	check = func(current *flow.Graph, prefix string) error {
		pure := flow.ValueSources(current, graph, metas)
		for _, node := range current.Nodes {
			if node.Type != flow.NodeTypeScript {
				continue
			}
			var cfg struct {
				Language string `json:"language"`
				Script   string `json:"script"`
			}
			if err := json.Unmarshal(node.Config, &cfg); err != nil || strings.TrimSpace(cfg.Script) == "" {
				continue
			}
			language := normalizeLanguage(cfg.Language)
			if language != sandbox.LangJavaScript && language != sandbox.LangPython && language != sandbox.LangBash {
				continue
			}
			diagnostics, err := CheckSource(ctx, language, cfg.Script, pure[node.ID], metas)
			if err != nil {
				return fmt.Errorf("%s: %w", prefix+"node:"+node.ID, err)
			}
			if len(diagnostics) > 0 {
				findings[prefix+"node:"+node.ID] = diagnostics
			}
		}
		for id, subgraph := range current.Subgraphs {
			child := subgraph
			if err := check(&child, prefix+"subgraph:"+id+"/"); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(graph, ""); err != nil {
		return nil, err
	}
	return findings, nil
}

func normalizeLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return sandbox.LangJavaScript
	}
	return language
}
