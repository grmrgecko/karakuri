package render

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type browserIssue struct {
	Kind    string `json:"kind"`
	URL     string `json:"url,omitempty"`
	Message string `json:"message,omitempty"`
	Status  int    `json:"status,omitempty"`
}

type captureInspection struct {
	mu        sync.Mutex
	issues    []browserIssue
	requests  map[proto.NetworkRequestID]string
	truncated bool
	geometry  any
	stop      func()
	token     string
}

var diagnosticURL = regexp.MustCompile(`(?:https?|wss?)://[^\s"'<>]+`)

// diagnosticText removes URL credentials and query strings before storing browser output.
func diagnosticText(text, token string) string {
	if token != "" {
		text = strings.ReplaceAll(text, token, "[redacted]")
	}
	text = diagnosticURL.ReplaceAllStringFunc(text, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[url]"
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	})
	if len(text) > 2000 {
		text = text[:2000] + "…"
	}
	return text
}

func startInspection(page *rod.Page, token string) *captureInspection {
	c := &captureInspection{issues: []browserIssue{}, requests: map[proto.NetworkRequestID]string{}, token: token}
	ctx, cancel := context.WithCancel(page.GetContext())
	done := make(chan struct{})
	wait := page.Context(ctx).EachEvent(
		func(e *proto.RuntimeExceptionThrown) {
			message := e.ExceptionDetails.Text
			if e.ExceptionDetails.Exception != nil {
				message += " " + e.ExceptionDetails.Exception.Description
			}
			c.add(browserIssue{Kind: "exception", Message: message, URL: e.ExceptionDetails.URL})
		},
		func(e *proto.RuntimeConsoleAPICalled) {
			if e.Type != proto.RuntimeConsoleAPICalledTypeError {
				return
			}
			parts := []string{}
			for _, arg := range e.Args {
				if arg.Description != "" {
					parts = append(parts, arg.Description)
				} else {
					parts = append(parts, arg.Value.String())
				}
			}
			c.add(browserIssue{Kind: "console_error", Message: strings.Join(parts, " ")})
		},
		func(e *proto.NetworkRequestWillBeSent) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if len(c.requests) < 2000 {
				c.requests[e.RequestID] = diagnosticText(e.Request.URL, token)
			} else {
				c.truncated = true
			}
		},
		func(e *proto.NetworkResponseReceived) {
			if e.Response.Status >= 400 {
				c.add(browserIssue{Kind: "http_error", URL: e.Response.URL, Status: e.Response.Status})
			}
		},
		func(e *proto.NetworkLoadingFailed) {
			c.mu.Lock()
			u := c.requests[e.RequestID]
			delete(c.requests, e.RequestID)
			c.mu.Unlock()
			c.add(browserIssue{Kind: "request_failed", URL: u, Message: e.ErrorText})
		},
		func(e *proto.NetworkLoadingFinished) { c.mu.Lock(); delete(c.requests, e.RequestID); c.mu.Unlock() },
	)
	go func() { defer close(done); wait() }()
	c.stop = func() { cancel(); <-done }
	return c
}

func (c *captureInspection) add(issue browserIssue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.issues) >= 100 {
		c.truncated = true
		return
	}
	issue.URL = diagnosticText(issue.URL, c.token)
	issue.Message = diagnosticText(issue.Message, c.token)
	c.issues = append(c.issues, issue)
}

func (c *captureInspection) measure(page *rod.Page) {
	result, err := page.Eval(inspectGeometry)
	if err != nil {
		c.add(browserIssue{Kind: "inspection_error", Message: err.Error()})
		return
	}
	var geometry any
	if err := result.Value.Unmarshal(&geometry); err != nil {
		c.add(browserIssue{Kind: "inspection_error", Message: err.Error()})
		return
	}
	c.geometry = geometry
}

func (c *captureInspection) report() any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{"layout": c.geometry, "browser_issues": c.issues, "truncated": c.truncated}
}

// inspectGeometry traverses open shadow roots because widget content lives inside them.
const inspectGeometry = `() => {
 const rect = e => { const r=e.getBoundingClientRect(); return {x:r.x,y:r.y,width:r.width,height:r.height}; };
 let scanned=0, truncated=false;
 const cells=[...document.querySelectorAll('[data-dashboard-widget]')];
 const widgets=cells.slice(0,500).map(cell=>{
  const bounds=rect(cell), overflow=[];
  const visit=root=>{
   for(const e of root.children||[]){
    if(++scanned>10000){truncated=true;return;}
    const style=getComputedStyle(e), r=rect(e);
    if(r.width>0 && r.height>0 && style.display!=='none' && style.visibility!=='hidden'){
     const x=e.scrollWidth>e.clientWidth+1, y=e.scrollHeight>e.clientHeight+1;
     if(x||y){if(overflow.length<25) overflow.push({tag:e.tagName.toLowerCase(),bounds:r,horizontal:x,vertical:y,overflow_x:style.overflowX,overflow_y:style.overflowY});else truncated=true;}
     if(e.shadowRoot)visit(e.shadowRoot);
     visit(e);
    }
   }
  };
  visit(cell);
  return {id:cell.dataset.dashboardWidget,package:cell.dataset.widgetPackage,bounds,state:cell.querySelector('[data-widget-state]')?.dataset.widgetState||'unknown',outside_viewport:bounds.x<0||bounds.y<0||bounds.x+bounds.width>innerWidth+1||bounds.y+bounds.height>innerHeight+1,overflow};
 });
 return {ready:!!document.querySelector('[data-dashboard-ready="true"]'),viewport:{width:innerWidth,height:innerHeight},document:{width:document.documentElement.scrollWidth,height:document.documentElement.scrollHeight},widgets,truncated:truncated||cells.length>500};
}`
