package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type Article struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

var (
	articles = make(map[string]Article)
	mutex    = &sync.Mutex{}
	filePath = "data/articles.json"
)

func printArticlesCount() {
	log.WithField("count", len(articles)).Info("Number of loaded articles")
}

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
		log.WithError(err).Error("Failed to save articles")
		return
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.Encode(articles)
	log.Info("Articles saved successfully")
}

func loadArticles() {
	log.WithField("filePath", filePath).Info("Attempting to load articles")
	file, err := os.Open(filePath)
	if err != nil {
		log.WithError(err).Warn("No saved articles found")
		return
	}
	defer file.Close()

	mutex.Lock()
	articles = make(map[string]Article)
	mutex.Unlock()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&articles); err != nil {
		log.WithError(err).Error("Failed to load articles")
		return
	}

	printArticlesCount()
}

func createArticleHandler(w http.ResponseWriter, r *http.Request) {
	logger := LogRequest(r)

	if r.Method == http.MethodGet {
		newID := generateID()
		logger.WithField("new_id", newID).Debug("Generated new article ID")

		tmpl, err := template.ParseFiles("create.html")
		if err != nil {
			logger.WithError(err).Error("Failed to parse create template")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, struct {
			ID string
		}{
			ID: newID,
		})
		return
	}

	if r.Method == http.MethodPost {
		id := r.FormValue("id")
		title := r.FormValue("title")

		article := Article{
			ID:        id,
			Title:     title,
			Content:   r.FormValue("content"),
			CreatedAt: time.Now(),
		}

		logger.WithFields(logrus.Fields{
			"article_id": article.ID,
			"title":      article.Title,
		}).Info("Creating new article")

		mutex.Lock()
		articles[article.ID] = article
		mutex.Unlock()
		saveArticles()
		http.Redirect(w, r, "/article?id="+article.ID, http.StatusSeeOther)
	}
}

func getArticleHandler(w http.ResponseWriter, r *http.Request) {
	logger := LogRequest(r)

	id := r.URL.Query().Get("id")
	if id == "" {
		logger.Warn("Missing article ID in request")
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}

	mutex.Lock()
	article, exists := articles[id]
	mutex.Unlock()

	if !exists {
		logger.WithField("article_id", id).Warn("Article not found")
		http.Error(w, "Article not found", http.StatusNotFound)
		return
	}

	logger.WithField("article_id", id).Debug("Retrieving article")

	// Create a template function map with a formatDate function
	funcMap := template.FuncMap{
		"formatDate": func(t time.Time) string {
			return t.Format("January 2, 2006 at 15:04")
		},
	}

	// !!! Hier wird der Content als HTML "markiert" !!!
	type ViewArticle struct {
		ID        string
		Title     string
		Content   template.HTML
		CreatedAt time.Time
	}

	viewArticle := ViewArticle{
		ID:        article.ID,
		Title:     article.Title,
		Content:   template.HTML(article.Content),
		CreatedAt: article.CreatedAt,
	}

	// Parse the template with the function map
	tmpl, _ := template.New("article.html").Funcs(funcMap).ParseFiles("article.html")
	tmpl.Execute(w, viewArticle)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	logger := LogRequest(r)

	id := r.URL.Query().Get("id")
	if id == "" {
		logger.Warn("Missing ID in upload request")
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}

	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		logger.WithError(err).Error("Failed to parse multipart form")
		http.Error(w, "File too large", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		logger.WithError(err).Error("Failed to retrieve file")
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	dirPath := "./uploads/" + id
	if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {
		logger.WithError(err).Error("Failed to create upload directory")
		http.Error(w, "Unable to create directory", http.StatusInternalServerError)
		return
	}

	filePath := dirPath + "/" + handler.Filename
	dst, err := os.Create(filePath)
	if err != nil {
		logger.WithError(err).Error("Failed to create file")
		http.Error(w, "Unable to create the file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	_, err = dst.ReadFrom(file)
	if err != nil {
		logger.WithError(err).Error("Failed to save file")
		http.Error(w, "Unable to save the file", http.StatusInternalServerError)
		return
	}

	logger.WithFields(logrus.Fields{
		"article_id": id,
		"filename":   handler.Filename,
	}).Info("File uploaded successfully")

	url := "/uploads/" + id + "/" + handler.Filename
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"location":"%s"}`, url)
}

func overviewHandler(w http.ResponseWriter, r *http.Request) {
	logger := LogRequest(r)
	logger.Debug("Overview handler called")

	mutex.Lock()
	articleSlice := make([]Article, 0, len(articles))
	for _, article := range articles {
		articleSlice = append(articleSlice, article)
	}
	mutex.Unlock()

	logger.WithField("article_count", len(articleSlice)).Debug("Preparing articles for display")

	sort.Slice(articleSlice, func(i, j int) bool {
		return articleSlice[i].CreatedAt.After(articleSlice[j].CreatedAt)
	})

	funcMap := template.FuncMap{
		"formatDate": func(t time.Time) string {
			return t.Format("02.01.2006 15:04")
		},
		"truncate": func(s string, length int) string {
			plainText := regexp.MustCompile("<[^>]*>").ReplaceAllString(s, "")
			if len(plainText) <= length {
				return plainText
			}
			return plainText[:length] + "..."
		},
	}

	tmpl := template.Must(template.New("overview.html").Funcs(funcMap).ParseFiles("overview.html"))
	tmpl.Execute(w, articleSlice)
}

func main() {
	initLogger()
	loadArticles()

	http.HandleFunc("/", createArticleHandler)
	http.HandleFunc("/article", getArticleHandler)
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/overview", overviewHandler)

	http.Handle("/tinymce/", http.StripPrefix("/tinymce/", http.FileServer(http.Dir("./tinymce"))))
	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("./uploads"))))

	log.Info("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
