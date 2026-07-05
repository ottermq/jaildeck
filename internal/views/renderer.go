package views

import (
	"net/http"
	"text/template"
)

type Renderer struct {
	pages map[string]*template.Template
}

func NewRenderer() (*Renderer, error) {
	jailPage, err := template.ParseFiles(
		"web/templates/layouts/base.html",
		"web/templates/pages/jails.html",
		"web/templates/components/jail_row.html",
	)
	if err != nil {
		return nil, err
	}

	return &Renderer{
		pages: map[string]*template.Template{
			"jails": jailPage,
		},
	}, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) error {
	tmpl, ok := r.pages[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return nil
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	return tmpl.ExecuteTemplate(w, "layouts/base.html", data)
}

func (r *Renderer) RenderComponent(w http.ResponseWriter, pageName string, componentName string, data any) error {
	tmpl, ok := r.pages[pageName]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return nil
	}

	w.Header().Set("Content-type", "text/html; charset=utf-8")

	return tmpl.ExecuteTemplate(w, componentName, data)
}
