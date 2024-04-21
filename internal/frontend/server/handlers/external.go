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
	// Инициализируем срез содержащий пути к двум файлам. Обратите внимание, что
	// файл home.page.tmpl должен быть *первым* файлом в срезе.
	files := []string{
		"../../pages/auth/auth.page.tmpl",
		"../../pages/base.layout.tmpl",
		"../../pages/header.partial.tmpl",
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

func HomeHandler(w http.ResponseWriter, r *http.Request) {}
func SendHandlerExternal(w http.ResponseWriter, r *http.Request) {}
func GetExpHandlerExternal(w http.ResponseWriter, r *http.Request) {}
func TimeValuesHandlerExternal(w http.ResponseWriter, r *http.Request) {}
func MonitorHandlerExternal(w http.ResponseWriter, r *http.Request) {}
