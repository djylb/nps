package ui

import (
	"bytes"
	"fmt"
	"html/template"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

type Page struct {
	Controller string
	Action     string
	Template   string
	Layout     string
	Data       map[string]any
}

type Model struct {
	Controller string         `json:"controller"`
	Action     string         `json:"action"`
	TplName    string         `json:"tpl_name"`
	Layout     string         `json:"layout,omitempty"`
	Data       map[string]any `json:"data"`
}

var (
	rootMu     sync.RWMutex
	rootPath   string
	templateMu sync.RWMutex
	templates  = make(map[string]*template.Template)
)

func SetRoot(root string) {
	rootMu.Lock()
	rootPath = root
	rootMu.Unlock()

	templateMu.Lock()
	templates = make(map[string]*template.Template)
	templateMu.Unlock()
}

func RenderToString(page *Page) (string, error) {
	if page == nil || page.Template == "" {
		return "", nil
	}
	root := currentRoot()
	if root == "" {
		return "", fmt.Errorf("view root not configured")
	}

	data := cloneData(page.Data)
	if page.Layout == "" {
		return execute(filepath.Join(root, page.Template), data)
	}

	content, err := execute(filepath.Join(root, page.Template), data)
	if err != nil {
		return "", err
	}
	layoutData := cloneData(data)
	layoutData["LayoutContent"] = template.HTML(content)
	return execute(filepath.Join(root, page.Layout), layoutData)
}

func PageModel(page *Page) *Model {
	if page == nil {
		return nil
	}
	return &Model{
		Controller: page.Controller,
		Action:     page.Action,
		TplName:    page.Template,
		Layout:     page.Layout,
		Data:       cloneData(page.Data),
	}
}

func currentRoot() string {
	rootMu.RLock()
	defer rootMu.RUnlock()
	return rootPath
}

func execute(path string, data map[string]any) (string, error) {
	tpl, err := templateAt(path)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func templateAt(path string) (*template.Template, error) {
	templateMu.RLock()
	if tpl, ok := templates[path]; ok {
		templateMu.RUnlock()
		return tpl, nil
	}
	templateMu.RUnlock()

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tpl, err := template.New(filepath.Base(path)).Parse(string(content))
	if err != nil {
		return nil, err
	}

	templateMu.Lock()
	templates[path] = tpl
	templateMu.Unlock()
	return tpl, nil
}

func cloneData(data map[string]any) map[string]any {
	if data == nil {
		return make(map[string]any)
	}
	cloned := make(map[string]any, len(data)+1)
	maps.Copy(cloned, data)
	return cloned
}
