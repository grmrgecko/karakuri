// Package docs holds the reference documentation shipped inside the binary and
// renders it for the API to serve. The markdown files are the source an author
// edits; the HTML is derived once per process and handed to the editor, so the
// frontend carries no markdown parser and the docs cannot drift from the build
// they were compiled into.
package docs

import (
	"bytes"
	"embed"
	"sync"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed *.md
var files embed.FS

// Guide describes one guide shipped in the binary, as the help index and the
// MCP resources list it.
type Guide struct {
	ID      string `json:"id" description:"Guide identifier, the file name without .md."`
	Title   string `json:"title" description:"The guide's title."`
	Group   string `json:"group" description:"The help index section the guide is listed under."`
	Summary string `json:"summary" description:"One sentence on what the guide covers."`
}

// catalog lists every guide in the order the help index shows them. A guide
// file without an entry here is not served, and the package tests fail on it.
var catalog = []Guide{
	{"getting-started", "Installing Karakuri", "Getting started", "Installing with Docker Compose, a release archive, or from source, and using SQLite, PostgreSQL, or MySQL."},
	{"home", "Home and overview", "Getting started", "The start page, the overview of what needs attention, and pinned dashboards."},
	{"flow-authoring", "Authoring flow graphs", "Flows", "The flow graph format, port typing, the draft, deploy, and enable lifecycle, triggers, failure handling, and sharing."},
	{"scripts", "Scripting", "Flows", "The script node sandbox: what a script reads and returns, what it may reach, and calling nodes from JavaScript, Python, and Bash."},
	{"ai", "AI nodes", "Flows", "AI provider connections, the model and agent nodes, tools, memory, and editing flows and dashboards with AI."},
	{"agent-nodes", "Agent nodes", "Flows", "Remote agents: enrollment, commands, files, services, and desktop and GUI automation on agent hosts."},
	{"browser-prompts", "Browser prompts", "Flows", "Asking a person a question in a browser from a flow and acting on the answer."},
	{"network-sockets", "Network socket nodes", "Flows", "TCP, UDP, and WebSocket connections, framing, replies, and limits."},
	{"dashboard-authoring", "Authoring dashboards", "Dashboards", "Dashboard layout, sections, pages, themes, variables, commands, and widget configuration."},
	{"dashboard-security", "What a dashboard exposes", "Dashboards", "What viewers of a shared or public dashboard can read and operate, renders, and render keys."},
	{"homeassistant", "Home Assistant", "Devices and integrations", "Areas, floors, devices, and labels from Home Assistant, targeting rooms from flows, and its entities as devices."},
	{"openhab", "openHAB", "Devices and integrations", "The openHAB connection, items as devices, and the item trigger, command, and read nodes."},
	{"esphome", "ESPHome", "Devices and integrations", "Connecting ESPHome nodes, their entities as devices, node actions, and limits."},
	{"frigate", "Frigate NVR", "Devices and integrations", "Frigate cameras on dashboards, detection and review triggers, images, and camera switches."},
	{"device-auto-import", "Device auto import", "Devices and integrations", "Adding the devices a connection reports automatically, Home Assistant MQTT discovery, filters, and removing devices no longer reported."},
	{"mqtt-device-state-script", "The state script", "Devices and integrations", "The MQTT device state script: reading one message into the values it carries."},
	{"mqtt-device-payload-script", "The payload script", "Devices and integrations", "The MQTT device payload script: turning one control change into a topic and payload."},
	{"notifications", "Notification services", "Devices and integrations", "Notification providers, endpoints, sending from flows, and delivery behavior."},
	{"timeseries", "Time-series connections and history", "Devices and integrations", "Prometheus, InfluxDB, PostgreSQL, and Home Assistant history queries, history forwarding, and chart data."},
	{"energy", "Energy", "Devices and integrations", "Energy sources, groups, tariffs and costs, solar, charts, flow nodes, and history import."},
	{"configuration", "Server configuration", "Administration", "Where the server reads its settings, environment overrides, secrets, and every key."},
	{"backup", "Backup", "Administration", "What a backup holds, the schedule, restoring, moving between databases, and permissions."},
	{"events", "Event log", "Administration", "What the event log records, actors, retention, permissions, and the event nodes."},
	{"mcp", "MCP", "Administration", "Connecting MCP clients, API keys and their limits, the tools, and editor chat."},
	{"plugins", "Plugins", "Administration", "Writing plugins: the manifest, nodes, connections, widgets and their scope, and securing plugin routes."},
}

// Guides returns the catalog in display order.
func Guides() []Guide {
	return append([]Guide(nil), catalog...)
}

// find returns a guide's catalog entry.
func find(id string) (Guide, bool) {
	for _, g := range catalog {
		if g.ID == id {
			return g, true
		}
	}
	return Guide{}, false
}

// Markdown returns a guide's source, for callers that serve markdown rather
// than the rendering.
func Markdown(id string) (string, bool) {
	if _, ok := find(id); !ok {
		return "", false
	}
	data, err := files.ReadFile(id + ".md")
	if err != nil {
		return "", false
	}
	return string(data), true
}

// Document is one rendered guide.
type Document struct {
	// ID names the page for callers that request it by name.
	ID string `json:"id"`
	// Title is the page's first heading, for the viewer's title bar.
	Title string `json:"title"`
	// HTML is the rendered markdown, safe to insert directly: raw HTML in the
	// source is escaped rather than passed through.
	HTML string `json:"html"`
}

// rendered caches each guide's rendering. A guide's source never changes for
// the lifetime of the process.
var rendered sync.Map

// Get returns a guide rendered to HTML.
func Get(id string) (Document, bool) {
	if doc, ok := rendered.Load(id); ok {
		return doc.(Document), true
	}
	g, ok := find(id)
	if !ok {
		return Document{}, false
	}
	source, ok := Markdown(id)
	if !ok {
		return Document{}, false
	}
	doc := Document{ID: g.ID, Title: g.Title, HTML: Render(source)}
	rendered.Store(id, doc)
	return doc, true
}

// Render converts markdown to HTML. GFM covers the tables and fenced code the
// reference is written in, and heading IDs give the viewer anchors to jump to a
// language section with. Raw HTML is left escaped: the reference source is
// ours, but note text on a canvas is not, and a renderer that passes markup
// through would hand the editor an injection point the moment it renders
// something an author wrote.
//
// Fenced code is tokenized by chroma into classes rather than inline colors, so
// the viewer paints the tokens from the same theme variables the script editor
// uses. A fence with no language, or one chroma has no lexer for, renders as
// plain text rather than being guessed at.
//
// It is exported for the markdown endpoint, which renders text the server never
// compiled in: notes on a flow canvas go through the same pipeline the
// reference pages do, so a note reads the way the documentation does.
func Render(source string) string {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
			),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithXHTML()),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		// Convert only fails on a writer error, and a bytes.Buffer has none.
		return ""
	}
	return buf.String()
}
