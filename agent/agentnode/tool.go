package agentnode

import (
	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// The agent drives the desktop through the tools a session already has, the
// same way the automation module does. These helpers are re-exported from that
// module so both share one implementation — and, for MissingTool, one error
// type, so IsMissingTool recognizes a missing-tool failure whether it came from
// an agent node or from the module.

// ToolPath returns the first of the named programs found in PATH.
var ToolPath = toolsh.ToolPath

// HasTool reports whether any of the named programs is installed.
var HasTool = toolsh.HasTool

// RunTool runs a program and returns its stdout, an error carrying its stderr.
var RunTool = toolsh.RunTool

// RunToolInput is RunTool with stdin.
var RunToolInput = toolsh.RunToolInput

// StartDetached launches a program and does not wait for it.
var StartDetached = toolsh.StartDetached

// CapText truncates text to a byte budget without splitting a character.
var CapText = toolsh.CapText

// FirstLine trims a tool's complaint to its first line.
var FirstLine = toolsh.FirstLine

// MissingTool reports that none of the tools that could do the job are
// installed, naming them so the operator knows what to add.
var MissingTool = autoapi.MissingTool

// IsMissingTool reports whether a failure was nothing being installed.
var IsMissingTool = autoapi.IsMissingTool
