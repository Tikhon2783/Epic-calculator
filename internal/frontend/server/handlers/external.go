package handlers

import (
	"log"
	"net/http"
	"html/template"

	// "calculator/internal"
	// "calculator/internal/frontend/server/utils"
	// "calculator/internal/errors"
)

func AuthHandlerExternal(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/auth/auth.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/home/home.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func SendHandlerExternal(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/home/home.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func GetExpHandlerExternal(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/send-exp/send-exp.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func TimeValuesHandlerExternal(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/values/values.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func MonitorHandlerExternal(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/monitor/monitor.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

