package utils

import (
	"net/http"
	"fmt"
	"strings"
)

// Функция для вывода красивой таблицы в консоль
func PrintTable(table [][]string, w http.ResponseWriter) {
	// Находим максимальные длины столбцов
	columnLengths := make([]int, len(table[0]))
	for _, line := range table {
		for i, val := range line {
			columnLengths[i] = max(len(val), columnLengths[i])
		}
	}

	var lineLength int = 1 // +1 для последней границы "|" в ряду
	for _, c := range columnLengths {
		lineLength += c + 3 // +3 для доп символов: "| %s "
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Server does not support Flusher!",
			http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "+%s+\n", strings.Repeat("-", lineLength-2)) // Верхняя граница
	flusher.Flush()
	for i, line := range table {
		for j, val := range line {
			fmt.Fprintf(w, "| %-*s ", columnLengths[j], val)
			if j == len(line)-1 {
				fmt.Fprintf(w, "|\n")
				flusher.Flush()
			}
		}
		if i == 0 || i == len(table)-1 { // Заголовок или нижняя граница
			fmt.Fprintf(w, "+%s+\n", strings.Repeat("-", lineLength-2))
			flusher.Flush()
		}
	}
}
