package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
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
	articles  = make(map[string]Article)
	mutex     = &sync.Mutex{}
	filePath  = "data/articles.json"
	uploadDir = "uploads"
)

func printArticlesCount() {
	log.WithField("count", len(articles)).Info("Number of loaded articles")
}

// idPattern matches the IDs created by generateID. Every ID taken from a
// request must match it before it is used in a file path or map lookup.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)

func validID(id string) bool {
	return idPattern.MatchString(id)
}

func generateID() string {
	letters := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	id := make([]byte, 8)
	for i := range id {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			panic(err)
		}
		id[i] = letters[n.Int64()]
	}
	return string(id)
}

// randomFileName returns a random hex name, so client-supplied file names
// never reach the file system.
func randomFileName() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
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
		r.Body = http.MaxBytesReader(w, r.Body, maxArticleBytes)
		if err := r.ParseForm(); err != nil {
			logger.WithError(err).Warn("Failed to parse article form")
			if isTooLarge(err) {
				http.Error(w, "Article too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}

		id := r.FormValue("id")
		title := r.FormValue("title")

		if !validID(id) {
			logger.WithField("article_id", id).Warn("Invalid article ID")
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		article := Article{
			ID:        id,
			Title:     title,
			Content:   sanitizeHTML(r.FormValue("content")),
			CreatedAt: time.Now(),
		}

		if storageUsed()+int64(len(article.Title)+len(article.Content)) > storageQuota {
			logger.Warn("Storage quota exceeded, article rejected")
			http.Error(w, "Storage full", http.StatusInsufficientStorage)
			return
		}

		logger.WithFields(logrus.Fields{
			"article_id": article.ID,
			"title":      article.Title,
		}).Info("Creating new article")

		// Articles are immutable once published: check and insert under one
		// lock so that nobody can replace someone else's article.
		mutex.Lock()
		_, exists := articles[article.ID]
		if !exists {
			articles[article.ID] = article
		}
		mutex.Unlock()
		if exists {
			logger.WithField("article_id", article.ID).Warn("Article ID already taken")
			http.Error(w, "Article already exists", http.StatusConflict)
			return
		}
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

	type ViewArticle struct {
		ID        string
		Title     string
		Content   template.HTML
		CreatedAt time.Time
	}

	// Content is sanitized again on output so that articles saved before the
	// filter existed cannot run scripts either.
	viewArticle := ViewArticle{
		ID:        article.ID,
		Title:     article.Title,
		Content:   template.HTML(sanitizeHTML(article.Content)),
		CreatedAt: article.CreatedAt,
	}

	// Second line of defence: the article page itself needs no JavaScript.
	w.Header().Set("Content-Security-Policy", "script-src 'none'; object-src 'none'; base-uri 'none'")

	// Parse the template with the function map
	tmpl, _ := template.New("article.html").Funcs(funcMap).ParseFiles("article.html")
	tmpl.Execute(w, viewArticle)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	logger := LogRequest(r)

	id := r.URL.Query().Get("id")
	if !validID(id) {
		logger.WithField("article_id", id).Warn("Invalid ID in upload request")
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	mutex.Lock()
	_, published := articles[id]
	mutex.Unlock()
	if published {
		logger.WithField("article_id", id).Warn("Upload to published article")
		http.Error(w, "Article already published", http.StatusConflict)
		return
	}

	// Allow some room on top of the file for the multipart headers.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+64<<10)
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		logger.WithError(err).Error("Failed to parse multipart form")
		if isTooLarge(err) {
			http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Invalid upload", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		logger.WithError(err).Error("Failed to retrieve file")
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if handler.Size > maxUploadBytes {
		logger.WithField("size", handler.Size).Warn("Upload too large")
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return
	}

	// The file type is decided by the content, never by the file name.
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	ext, ok := imageTypes[http.DetectContentType(head[:n])]
	if !ok {
		logger.WithField("filename", handler.Filename).Warn("Upload is not an allowed image type")
		http.Error(w, "Only PNG, JPEG, GIF and WebP images are allowed", http.StatusUnsupportedMediaType)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		logger.WithError(err).Error("Failed to rewind upload")
		http.Error(w, "Unable to save the file", http.StatusInternalServerError)
		return
	}

	if storageUsed()+handler.Size > storageQuota {
		logger.Warn("Storage quota exceeded, upload rejected")
		http.Error(w, "Storage full", http.StatusInsufficientStorage)
		return
	}

	dirPath := filepath.Join(uploadDir, id)
	if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {
		logger.WithError(err).Error("Failed to create upload directory")
		http.Error(w, "Unable to create directory", http.StatusInternalServerError)
		return
	}

	fileName := randomFileName() + ext
	filePath := filepath.Join(dirPath, fileName)
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
		"stored_as":  fileName,
	}).Info("File uploaded successfully")

	url := "/uploads/" + id + "/" + fileName
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

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", createArticleHandler)
	mux.HandleFunc("/article", getArticleHandler)
	mux.HandleFunc("/upload", uploadHandler)
	mux.HandleFunc("/overview", overviewHandler)

	mux.Handle("/tinymce/", http.StripPrefix("/tinymce/", http.FileServer(http.Dir("./tinymce"))))
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", noActiveContent(http.FileServer(http.Dir(uploadDir)))))

	return newRateLimiter(rateLimit).limitWrites(mux)
}

// noActiveContent keeps browsers from running uploaded files as web pages.
// This also covers files uploaded before the content check existed.
func noActiveContent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		next.ServeHTTP(w, r)
	})
}

func main() {
	initLogger()

	addr, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	loadArticles()

	log.WithFields(logrus.Fields{
		"addr":             addr,
		"storage_quota_mb": storageQuota >> 20,
		"rate_limit":       rateLimit,
		"trust_proxy":      trustProxy,
	}).Info("Server starting")
	log.Fatal(http.ListenAndServe(addr, routes()))
}
