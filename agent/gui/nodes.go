package gui

import (
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// folder is the palette folder this group's nodes list under, nested under the
// agent's own palette group by the host.
const folder = "GUI"

// Nodes returns GUI actions and observations using the agent's shared session.
// Initialization errors surface on execution so headless hosts retain a catalog.
func Nodes(sess *automation.Session, err error) []nodeapi.NodeType {
	return []nodeapi.NodeType{
		&imageMatchNode{sess: sess, initErr: err},
		&interactionNode{kind: "drag-drop", sess: sess, initErr: err},
		&interactionNode{kind: "window-place", sess: sess, initErr: err},
		&screenshotNode{sess: sess, initErr: err},
		&windowListNode{sess: sess, initErr: err},
		&windowNode{sess: sess, initErr: err},
		&keyboardNode{sess: sess, initErr: err},
		&mouseNode{sess: sess, initErr: err},
		&freshNode{NodeType: &screenshotNode{sess: sess, initErr: err}, kind: "screenshot-now", label: "Screenshot now"},
		&freshNode{NodeType: &windowListNode{sess: sess, initErr: err}, kind: "window-list-now", label: "Window list now"},
		&inspectionNode{kind: "ui-snapshot", sess: sess, initErr: err},
		&inspectionNode{kind: "click-control", sess: sess, initErr: err},
		&inspectionNode{kind: "wait-window", sess: sess, initErr: err},
		&inspectionNode{kind: "wait-ui-state", sess: sess, initErr: err},
		&inspectionNode{kind: "menu-list", sess: sess, initErr: err},
		&inspectionNode{kind: "menu-invoke", sess: sess, initErr: err},
		&inspectionNode{kind: "display-list", sess: sess, initErr: err},
		&inspectionNode{kind: "display-mode", sess: sess, initErr: err},
		&inspectionNode{kind: "dialog-list", sess: sess, initErr: err},
	}
}
