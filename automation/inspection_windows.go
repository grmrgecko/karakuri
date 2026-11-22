//go:build windows

package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const inspectionBackend = "uia/user32"

type windowsInspection struct{}

func newInspection() Inspection { return windowsInspection{} }
func windowsUIA(ctx context.Context, w WindowInfo, id string) ([]Element, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	hwnd, err := strconv.ParseUint(strings.TrimPrefix(w.ID, "0x"), 16, 64)
	if err != nil {
		return nil, err
	}
	for _, c := range id {
		if (c < '0' || c > '9') && c != '.' && c != '-' {
			return nil, fmt.Errorf("invalid UIA element id")
		}
	}
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,WindowsBase,System.Web.Extensions
$source=@'
using System;
using System.Collections.Generic;
using System.Windows.Automation;
using System.Web.Script.Serialization;
public static class KarakuriInspect {
 static int Pixel(double value) { return Double.IsNaN(value) || Double.IsInfinity(value) || value<Int32.MinValue || value>Int32.MaxValue ? 0 : (int)value; }
 public static string Run(long handle, string wanted) {
  AutomationElement root=AutomationElement.FromHandle(new IntPtr(handle));
  AutomationElementCollection items=root.FindAll(TreeScope.Subtree,Condition.TrueCondition);
  List<Dictionary<string,object>> rows=new List<Dictionary<string,object>>();
  if(items.Count>4096) throw new Exception("accessibility tree exceeds 4096 elements");
  for(int i=0;i<items.Count;i++) {
   AutomationElement item=items[i];
   AutomationElement.AutomationElementInformation c=item.Current;
   int[] runtime=item.GetRuntimeId();
   string id=String.Join(".",Array.ConvertAll<int,string>(runtime,delegate(int v){return v.ToString();}));
   object action;
   if(wanted!="" && id==wanted) {
    if(!c.IsEnabled) throw new Exception("element is disabled");
    if(item.TryGetCurrentPattern(InvokePattern.Pattern,out action)) ((InvokePattern)action).Invoke();
    else if(item.TryGetCurrentPattern(TogglePattern.Pattern,out action)) ((TogglePattern)action).Toggle();
    else throw new Exception("element exposes no invoke or toggle pattern");
    return "[]";
   }
   Dictionary<string,object> row=new Dictionary<string,object>();
   row.Add("id",id);row.Add("name",c.Name);row.Add("role",c.ControlType.ProgrammaticName);row.Add("class",c.ClassName);
   row.Add("enabled",c.IsEnabled);row.Add("visible",!c.IsOffscreen);row.Add("protected",c.IsPassword);
   System.Windows.Rect r=System.Windows.Rect.Empty;
   try { r=c.BoundingRectangle; }
   catch(InvalidOperationException e) { row.Add("geometry_error",e.Message); }
   catch(ElementNotAvailableException e) { row.Add("geometry_error",e.Message); }
   row.Add("x",Pixel(r.X));row.Add("y",Pixel(r.Y));row.Add("width",Pixel(r.Width));row.Add("height",Pixel(r.Height));
   if(!c.IsPassword && item.TryGetCurrentPattern(ValuePattern.Pattern,out action)) row.Add("text",((ValuePattern)action).Current.Value);
   List<string> actions=new List<string>();
   if(item.TryGetCurrentPattern(InvokePattern.Pattern,out action))actions.Add("invoke");
   if(item.TryGetCurrentPattern(TogglePattern.Pattern,out action))actions.Add("toggle");row.Add("actions",actions);
   rows.Add(row);
  }
  if(wanted!="") throw new Exception("element no longer belongs to the window");
  JavaScriptSerializer serializer=new JavaScriptSerializer();serializer.MaxJsonLength=4194304;return serializer.Serialize(rows);
 }
}
'@
$references=@([System.Windows.Automation.AutomationElement].Assembly.Location,[System.Windows.Automation.ControlType].Assembly.Location,[System.Windows.Rect].Assembly.Location,[System.Web.Script.Serialization.JavaScriptSerializer].Assembly.Location)
Add-Type -TypeDefinition $source -ReferencedAssemblies $references
[Console]::WriteLine([KarakuriInspect]::Run(%d,'%s'))`, hwnd, id)
	command := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	data, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("UIA: %w: %s", err, data)
	}
	var out []Element
	err = json.Unmarshal([]byte(strings.TrimPrefix(string(data), "\ufeff")), &out)
	return out, err
}
func (windowsInspection) Elements(ctx context.Context, w WindowInfo) ([]Element, error) {
	return windowsUIA(ctx, w, "")
}
func (windowsInspection) Invoke(ctx context.Context, w WindowInfo, id string) error {
	_, err := windowsUIA(ctx, w, id)
	return err
}

var inspectionUser = windows.NewLazySystemDLL("user32.dll")

func (windowsInspection) Displays(ctx context.Context) ([]DisplayInfo, error) {
	var mode win.DEVMODE
	mode.DmSize = uint16(unsafe.Sizeof(mode))
	ok, _, err := inspectionUser.NewProc("EnumDisplaySettingsW").Call(0, uintptr(0xffffffff), uintptr(unsafe.Pointer(&mode)))
	if ok == 0 {
		return nil, err
	}
	return []DisplayInfo{{ID: "primary", Width: int(mode.DmPelsWidth), Height: int(mode.DmPelsHeight), Depth: int(mode.DmBitsPerPel), Refresh: float64(mode.DmDisplayFrequency), Primary: true}}, nil
}
func (windowsInspection) SetDisplay(ctx context.Context, m DisplayMode) error {
	if m.Width <= 0 || m.Height <= 0 || m.Depth <= 0 {
		return fmt.Errorf("display dimensions and depth must be positive")
	}
	mode := win.DEVMODE{DmPelsWidth: uint32(m.Width), DmPelsHeight: uint32(m.Height), DmBitsPerPel: uint32(m.Depth), DmFields: 0x80000 | 0x100000 | 0x40000}
	mode.DmSize = uint16(unsafe.Sizeof(mode))
	proc := inspectionUser.NewProc("ChangeDisplaySettingsW")
	for _, flags := range []uintptr{2, 0} {
		status, _, _ := proc.Call(uintptr(unsafe.Pointer(&mode)), flags)
		if int32(status) != 0 {
			return fmt.Errorf("display mode rejected (code %d)", int32(status))
		}
	}
	return nil
}
