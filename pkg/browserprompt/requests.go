// Package browserprompt holds transient, request-scoped browser questions.
package browserprompt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/docs"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// ErrGone refuses expired, answered, canceled and unknown capabilities equally.
var ErrGone = errors.New("browser prompt is no longer available")

// Response records the choice without changing the incoming payload.
type Response struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Text       string    `json:"text"`
	RequestID  string    `json:"request_id"`
	AnsweredAt time.Time `json:"answered_at"`
}

// Asset identifies one request-scoped attachment without exposing its bytes.
type Asset struct {
	Name     string `json:"name"`
	Filename string `json:"filename"`
	Type     string `json:"type"`
}

// View contains the question a browser may display.
type View struct {
	Title           string              `json:"title"`
	HTML            string              `json:"html"`
	Buttons         []flow.PromptButton `json:"buttons"`
	Feedback        bool                `json:"feedback"`
	RequireFeedback bool                `json:"require_feedback"`
	ExpiresAt       time.Time           `json:"expires_at"`
	ServerTime      time.Time           `json:"server_time"`
	Assets          []Asset             `json:"assets"`
}

// Request owns one immutable question and an atomic answer slot.
type Request struct {
	mu          sync.Mutex
	ctx         context.Context
	now         func() time.Time
	view        View
	attachments map[string]flow.Attachment
	id          string
	wake        chan struct{}
	response    *Response
	closed      bool
}

// Manager bounds the transient questions held by the server.
type Manager struct {
	mu       sync.Mutex
	requests map[string]*Request
	now      func() time.Time
}

// NewManager returns an empty request manager with a real clock.
func NewManager() *Manager { return &Manager{requests: map[string]*Request{}, now: time.Now} }

// Default serves browser prompt nodes in this server process.
var Default = NewManager()

// Create registers a question before the browser is launched.
func (m *Manager) Create(ctx context.Context, cfg flow.BrowserPromptConfig, timeout time.Duration) (string, *Request, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if problems := flow.ValidatePromptButtons(cfg.Buttons, false); len(problems) > 0 {
		return "", nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	if problems := flow.ValidateAttachments(cfg.Attachments); len(problems) > 0 {
		return "", nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", nil, fmt.Errorf("creating browser prompt token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", nil, fmt.Errorf("creating browser prompt id: %w", err)
	}
	r := &Request{ctx: ctx, now: m.now, id: base64.RawURLEncoding.EncodeToString(identity[:]), wake: make(chan struct{}), attachments: cfg.Attachments}
	r.view = View{Title: cfg.Title, HTML: docs.Render(cfg.Markdown), Buttons: append([]flow.PromptButton{}, cfg.Buttons...), Feedback: cfg.Feedback || cfg.RequireFeedback, RequireFeedback: cfg.RequireFeedback, ExpiresAt: m.now().Add(timeout), Assets: []Asset{}}
	for name, a := range cfg.Attachments {
		r.view.Assets = append(r.view.Assets, Asset{Name: name, Filename: a.Filename, Type: a.Type})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, request := range m.requests {
		if !request.available() {
			delete(m.requests, key)
		}
	}
	if len(m.requests) >= 128 {
		return "", nil, fmt.Errorf("too many pending browser prompts; answer or cancel an existing request")
	}
	m.requests[token] = r
	return token, r, nil
}

// Release removes a capability and closes its pending request.
func (m *Manager) Release(token string) {
	m.mu.Lock()
	r := m.requests[token]
	delete(m.requests, token)
	m.mu.Unlock()
	if r != nil {
		r.mu.Lock()
		r.close()
		r.mu.Unlock()
	}
}

func (r *Request) close() {
	if !r.closed {
		r.closed = true
		close(r.wake)
	}
}

func (r *Request) availableLocked() bool {
	if r.ctx.Err() != nil || !r.now().Before(r.view.ExpiresAt) {
		r.close()
	}
	return !r.closed && r.response == nil
}

func (r *Request) available() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.availableLocked() }

func (m *Manager) resolve(token string) (*Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.requests[token]
	if r == nil || !r.available() {
		delete(m.requests, token)
		return nil, ErrGone
	}
	return r, nil
}

// Read returns only the question authorized by token.
func (m *Manager) Read(token string) (View, error) {
	r, err := m.resolve(token)
	if err != nil {
		return View{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.availableLocked() {
		return View{}, ErrGone
	}
	view := r.view
	view.ServerTime = r.now()
	return view, nil
}

// Answer validates a response before consuming the capability.
func (m *Manager) Answer(token, button, text string) error {
	r, err := m.resolve(token)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.availableLocked() {
		return ErrGone
	}
	if len(text) > flow.MaxPromptFeedback {
		return fmt.Errorf("feedback exceeds 4096 bytes")
	}
	if r.view.RequireFeedback && strings.TrimSpace(text) == "" {
		return fmt.Errorf("write feedback before choosing a button")
	}
	if !r.view.Feedback && text != "" {
		return fmt.Errorf("this prompt does not accept written feedback")
	}
	for _, b := range r.view.Buttons {
		if b.ID == button {
			r.response = &Response{ID: b.ID, Label: b.Label, Text: text, RequestID: r.id, AnsweredAt: r.now()}
			r.close()
			return nil
		}
	}
	return fmt.Errorf("unknown browser prompt button")
}

// Attachment returns one pending request's file.
func (m *Manager) Attachment(token, name string) (flow.Attachment, []byte, error) {
	r, err := m.resolve(token)
	if err != nil {
		return flow.Attachment{}, nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.availableLocked() {
		return flow.Attachment{}, nil, ErrGone
	}
	a, ok := r.attachments[name]
	if !ok {
		return flow.Attachment{}, nil, ErrGone
	}
	b, err := flow.AttachmentBytes(a)
	return a, b, err
}

// Wake closes when the request is answered or becomes unavailable.
func (r *Request) Wake() <-chan struct{} { return r.wake }

// Result expires unanswered questions atomically with response submission.
func (r *Request) Result() (*Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ctx.Err(); err != nil {
		r.close()
		return nil, err
	}
	if r.response != nil {
		result := *r.response
		return &result, nil
	}
	r.close()
	return nil, fmt.Errorf("browser prompt answer timed out")
}

// Remaining reports the request's remaining lifetime.
func (r *Request) Remaining() time.Duration { return r.view.ExpiresAt.Sub(r.now()) }
