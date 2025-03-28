package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"
)

type Article struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

var (
	articles = make(map[string]Article)
	mutex    = &sync.Mutex{}
	filePath = "articles.json"
)

func generateID() string {
	rand.Seed(time.Now().UnixNano())
	letters := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	id := make([]byte, 8)
	for i := range id {
		id[i] = letters[rand.Intn(len(letters))]
	}
	return string(id)
}

func saveArticles() {
	mutex.Lock()
	defer mutex.Unlock()
	file, err := os.Create(filePath)
	if err != nil {
		log.Println("Fehler beim Speichern der Artikel:", err)
		return
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.Encode(articles)
}

func loadArticles() {
	file, err := os.Open(filePath)
	if err != nil {
		log.Println("Keine gespeicherten Artikel gefunden.")
		return
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&articles); err != nil {
		log.Println("Fehler beim Laden der Artikel:", err)
	}
}

func createArticleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		tmpl, _ := template.ParseFiles("create.html")
		tmpl.Execute(w, nil)
		return
	}

	if r.Method == http.MethodPost {
		title := r.FormValue("title")
		content := r.FormValue("content")
		article := Article{
			ID:      generateID(),
			Title:   title,
			Content: content,
		}

		mutex.Lock()
		articles[article.ID] = article
		mutex.Unlock()
		saveArticles()
		http.Redirect(w, r, "/article?id="+article.ID, http.StatusSeeOther)
	}
}

func getArticleHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}

	mutex.Lock()
	article, exists := articles[id]
	mutex.Unlock()

	if !exists {
		http.Error(w, "Article not found", http.StatusNotFound)
		return
	}

	tmpl, _ := template.ParseFiles("article.html")
	tmpl.Execute(w, article)
}

func main() {
	loadArticles()
	http.HandleFunc("/", createArticleHandler)
	http.HandleFunc("/article", getArticleHandler)

	fmt.Println("Server running on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
