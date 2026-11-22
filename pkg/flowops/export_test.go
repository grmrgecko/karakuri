package flowops

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRegistry maps node types to metadata for blanking tests.
type stubRegistry map[string]flow.NodeMeta

func (r stubRegistry) MetaFor(nodeType string) (flow.NodeMeta, bool) {
	m, ok := r[nodeType]
	return m, ok
}

// TestBlankConnectionFields covers what an exported flow must not carry. A
// connection id names a broker or a Home Assistant instance that exists only on
// the exporting server, so an import elsewhere would either fail or — worse —
// resolve to whatever connection happened to share the id. Blanking the field
// makes the importer choose, which is the same reason a dashboard export names
// widgets by package rather than by local id.
//
// The fields are found through the node type's config schema, so the two rows
// that matter are a node inside a subgraph, which a shallow walk would miss,
// and a node type this build does not know, whose config must be left exactly
// as it is rather than guessed at.
func TestBlankConnectionFields(t *testing.T) {
	reg := stubRegistry{
		"mqtt-out": {
			Type: "mqtt-out",
			ConfigSpec: []flow.FieldSpec{
				{ID: "connection", Type: flow.FieldConnection, Required: true},
				{ID: "topic", Type: flow.FieldString, Required: true},
			},
		},
	}
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "a", Type: "mqtt-out", Config: json.RawMessage(`{"connection":"11111111-1111-1111-1111-111111111111","topic":"home/x"}`)},
			{ID: "unknown", Type: "mystery", Config: json.RawMessage(`{"connection":"keep"}`)},
		},
		Subgraphs: map[string]flow.Graph{
			"sg": {
				Nodes: []flow.Node{
					{ID: "b", Type: "mqtt-out", Config: json.RawMessage(`{"connection":"22222222-2222-2222-2222-222222222222","topic":"home/y"}`)},
				},
			},
		},
	}

	blankConnectionFields(g, reg)

	check := func(where string, raw json.RawMessage, wantTopic string) {
		t.Helper()
		var cfg map[string]any
		require.NoErrorf(t, json.Unmarshal(raw, &cfg), "%s: the rewritten config must still be JSON", where)

		assert.Emptyf(t, cfg["connection"],
			"%s: the connection names a server that exists only here, so it must not travel", where)
		assert.Equalf(t, wantTopic, cfg["topic"],
			"%s: only the connection is blanked; the rest of the config is the author's work", where)
	}
	check("top level", g.Nodes[0].Config, "home/x")
	check("inside a subgraph", g.Subgraphs["sg"].Nodes[0].Config, "home/y")

	var cfg map[string]any
	require.NoError(t, json.Unmarshal(g.Nodes[1].Config, &cfg), "the untouched config must still be JSON")
	assert.Equal(t, "keep", cfg["connection"],
		"a node type this build does not know has no schema to read, so its config must be left exactly as it is")
}

// TestBlankConnectionFieldsKeepsNumbers locks that blanking a connection
// leaves the rest of the node's config as written. The config was decoded with
// numbers as floats and written back, so an export rounded every integer past
// 2^53 in fields it had no reason to touch.
func TestBlankConnectionFieldsKeepsNumbers(t *testing.T) {
	reg := stubRegistry{
		"notify": {
			Type: "notify",
			ConfigSpec: []flow.FieldSpec{
				{ID: "connection", Type: flow.FieldConnection, Required: true},
				{ID: "channel", Type: flow.FieldNumber},
			},
		},
	}
	g := &flow.Graph{Nodes: []flow.Node{
		{ID: "a", Type: "notify", Config: json.RawMessage(`{"connection":"11111111-1111-1111-1111-111111111111","channel":1234567890123456789,"nested":{"id":9007199254740993}}`)},
	}}

	blankConnectionFields(g, reg)

	var cfg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(g.Nodes[0].Config, &cfg), "the rewritten config must still be a JSON object")
	assert.Equal(t, `""`, string(cfg["connection"]), "the connection is still blanked")
	assert.Equal(t, `1234567890123456789`, string(cfg["channel"]), "an integer past 2^53 in another field must be exported as written")
	assert.Contains(t, string(cfg["nested"]), "9007199254740993", "a nested integer past 2^53 must keep its digits")
}

// TestExportCarriesSubflowFaceplateOverrides locks that an exported flow keeps
// the faceplate values applied on a shared subflow's calling node. Export
// turns the shared subflow into a local subgraph, where values stored on the
// calling node are no longer read, so the document ran on the subflow's own
// values and the importer saw settings the exporter never had. A second
// caller without overrides must keep the subflow's own values.
func TestExportCarriesSubflowFaceplateOverrides(t *testing.T) {
	registerOnce.Do(func() {
		core.RegisterAll(engine.Default())
		engine.Default().Register(sharedSink)
	})
	require.NoError(t, db.Init(db.Options{Type: "sqlite", DSN: memdb.TestDB(t), MaxOpenConns: 1, MaxIdleConns: 1}))
	require.NoError(t, migrate.AutoMigrate())
	suffix := uuid.NewString()[:8]
	owner := &models.User{ID: uuid.New(), Username: "owner-" + suffix, Email: suffix + "@example.test", PasswordHash: "x"}
	require.NoError(t, db.DB.Create(owner).Error)

	sub, err := Create(context.Background(), "sub "+suffix, "", true, nil, owner.ID)
	require.NoError(t, err)
	require.NoError(t, SaveDraft(sub, &flow.Graph{Nodes: []flow.Node{{
		ID: "fp", Type: flow.NodeTypeFaceplate,
		Config: json.RawMessage(`{"name":"Settings","controls":[{"id":"level","label":"Level","type":"number","default":50}],"values":{"level":60}}`),
	}}}))
	_, err = Deploy(t.Context(), sub, "test", owner.ID)
	require.NoError(t, err, "the shared subflow must deploy to be callable")

	parent, err := Create(context.Background(), "parent "+suffix, "", false, nil, owner.ID)
	require.NoError(t, err)
	require.NoError(t, SaveDraft(parent, &flow.Graph{Nodes: []flow.Node{
		{ID: "tuned", Type: flow.NodeTypeSubflow, SubflowID: &sub.ID,
			Config: json.RawMessage(`{"faceplate_values":{"fp":{"level":70}}}`)},
		{ID: "plain", Type: flow.NodeTypeSubflow, SubflowID: &sub.ID},
	}}))

	doc, err := Export(context.Background(), parent, owner.ID)
	require.NoError(t, err, "the owner may export a flow calling their own subflow")

	level := func(nodeID string) any {
		for _, n := range doc.Graph.Nodes {
			if n.ID != nodeID {
				continue
			}
			for _, inner := range doc.Graph.Subgraphs[n.SubgraphID].Nodes {
				cfg, err := flow.ParseFaceplate(inner.Config)
				require.NoError(t, err, "the embedded faceplate must still decode")
				return cfg.Values["level"]
			}
		}
		return nil
	}
	assert.Equal(t, 70.0, level("tuned"), "the calling node's applied value travels into its embedded copy")
	assert.Equal(t, 60.0, level("plain"), "a caller without overrides keeps the subflow's own value")
}
