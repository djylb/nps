package ui

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sync"
)

type Page struct {
	Controller string
	Action     string
	Template   string
	Layout     string
	Data       map[string]interface{}
}

type Model struct {
	Controller string                 `json:"controller"`
	Action     string                 `json:"action"`
	TplName    string                 `json:"tpl_name"`
	Layout     string                 `json:"layout,omitempty"`
	Data       map[string]interface{} `json:"data"`
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

func execute(path string, data map[string]interface{}) (string, error) {
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

func cloneData(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return make(map[string]interface{})
	}
	cloned := make(map[string]interface{}, len(data)+1)
	for key, value := range data {
		cloned[key] = value
	}
	return cloned
}
