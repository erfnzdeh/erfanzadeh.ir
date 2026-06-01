package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexFS embed.FS

const maxUploadBytes = 10 << 30 // 10 GB for user uploads

type fileEntry struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Type      string `json:"type"`
	MTime     string `json:"mtime"`
	Permanent bool   `json:"permanent"`
	Downloads int    `json:"downloads"`
	IP        string `json:"ip,omitempty"`
}

type server struct {
	assetsDir  string
	uploadsDir string
	countsFile string
	ipsFile    string
	mu         sync.Mutex
	counts     map[string]int
	ips        map[string]string
}

func (s *server) loadCounts() {
	s.counts = make(map[string]int)
	data, err := os.ReadFile(s.countsFile)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.counts)
}

func (s *server) saveCounts() {
	data, _ := json.Marshal(s.counts)
	os.WriteFile(s.countsFile, data, 0644)
}

func (s *server) loadIPs() {
	s.ips = make(map[string]string)
	data, err := os.ReadFile(s.ipsFile)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.ips)
}

func (s *server) saveIPs() {
	data, _ := json.Marshal(s.ips)
	os.WriteFile(s.ipsFile, data, 0644)
}

func (s *server) setIP(name, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ips[name] = ip
	s.saveIPs()
}

func (s *server) incDownload(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[name]++
	s.saveCounts()
}

func listDir(dir string, permanent bool) ([]fileEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []fileEntry
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{
			Name:      info.Name(),
			Size:      info.Size(),
			Type:      "file",
			MTime:     info.ModTime().UTC().Format(time.RFC3339),
			Permanent: permanent,
		})
	}
	return files, nil
}

func (s *server) allFiles() ([]fileEntry, error) {
	perm, err := listDir(s.assetsDir, true)
	if err != nil {
		return nil, err
	}
	temp, err := listDir(s.uploadsDir, false)
	if err != nil {
		return nil, err
	}
	all := append(perm, temp...)
	s.mu.Lock()
	for i := range all {
		all[i].Downloads = s.counts[all[i].Name]
		all[i].IP = s.ips[all[i].Name]
	}
	s.mu.Unlock()
	return all, nil
}

func uploadsSize(dir string) (int64, error) {
	files, err := listDir(dir, false)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return total, nil
}

// evictionAge is how old an upload must be before it becomes a candidate for
// download-count-based eviction. Files younger than this are protected.
const evictionAge = 7 * 24 * time.Hour

// pickVictim chooses which upload to delete to free space. Among files older
// than evictionAge it picks the least-downloaded (oldest wins ties). If no file
// is old enough, it falls back to evicting the oldest file regardless of age.
func (s *server) pickVictim(files []fileEntry) fileEntry {
	cutoff := time.Now().Add(-evictionAge)

	var victim fileEntry
	found := false
	for _, f := range files {
		t, err := time.Parse(time.RFC3339, f.MTime)
		if err != nil || !t.Before(cutoff) {
			continue // unparseable or younger than 7d: protected
		}
		if !found {
			victim, found = f, true
			continue
		}
		c, vc := s.counts[f.Name], s.counts[victim.Name]
		if c < vc || (c == vc && f.MTime < victim.MTime) {
			victim = f
		}
	}
	if found {
		return victim
	}

	// Fallback: nothing is old enough, evict the oldest file overall.
	oldest := files[0]
	for _, f := range files[1:] {
		if f.MTime < oldest.MTime {
			oldest = f
		}
	}
	return oldest
}

func (s *server) enforce(needed int64) error {
	for {
		total, err := uploadsSize(s.uploadsDir)
		if err != nil {
			return err
		}
		if total+needed <= maxUploadBytes {
			return nil
		}
		files, err := listDir(s.uploadsDir, false)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("cannot free space: no upload files to delete")
		}
		victim := s.pickVictim(files)
		log.Printf("quota: removing upload %s (%d bytes, %d downloads) to make room",
			victim.Name, victim.Size, s.counts[victim.Name])
		if err := os.Remove(filepath.Join(s.uploadsDir, victim.Name)); err != nil {
			return err
		}
	}
}

// presentSizes returns a name->size map for every file still on disk
// (uploads + permanent assets) plus the average size across them.
func (s *server) presentSizes() (map[string]int64, int64) {
	sizes := make(map[string]int64)
	var sum, n int64
	add := func(files []fileEntry) {
		for _, f := range files {
			if _, ok := sizes[f.Name]; ok {
				continue
			}
			sizes[f.Name] = f.Size
			sum += f.Size
			n++
		}
	}
	if files, err := listDir(s.uploadsDir, false); err == nil {
		add(files)
	}
	if files, err := listDir(s.assetsDir, true); err == nil {
		add(files)
	}
	var avg int64
	if n > 0 {
		avg = sum / n
	}
	return sizes, avg
}

// handleStats reports lifetime metrics derived entirely from the data we've
// always recorded: per-file download counts and uploader records. Bytes are
// estimated (we never logged real transfer): files still on disk contribute
// exact size*count, and everything since evicted is estimated as the average
// file size times its downloads. Upload bytes are estimated the same way.
func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	sizes, avg := s.presentSizes()

	s.mu.Lock()
	var totalDownloads, knownBytes, knownDownloads int64
	for name, c := range s.counts {
		totalDownloads += int64(c)
		if sz, ok := sizes[name]; ok {
			knownBytes += sz * int64(c)
			knownDownloads += int64(c)
		}
	}
	totalFiles := int64(len(s.ips))
	seen := make(map[string]struct{}, len(s.ips))
	for _, ip := range s.ips {
		if ip != "" {
			seen[ip] = struct{}{}
		}
	}
	uniqueIPs := int64(len(seen))
	s.mu.Unlock()

	unknownDownloads := totalDownloads - knownDownloads
	if unknownDownloads < 0 {
		unknownDownloads = 0
	}

	stats := map[string]int64{
		"bytes_down": knownBytes + avg*unknownDownloads,
		"bytes_up":   avg * totalFiles,
		"downloads":  totalDownloads,
		"uploads":    totalFiles,
		"unique_ips": uniqueIPs,
		"estimated":  1,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	files, err := s.allFiles()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if files == nil {
		files = []fileEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if parts := strings.SplitN(xff, ",", 2); len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return xri
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "file too large or bad request", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if header.Size > maxUploadBytes {
		http.Error(w, "file exceeds 2GB limit", http.StatusRequestEntityTooLarge)
		return
	}

	name := filepath.Base(header.Filename)
	if name == "." || name == "/" {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.enforce(header.Size); err != nil {
		http.Error(w, "storage full: "+err.Error(), http.StatusInsufficientStorage)
		return
	}

	dst, err := os.Create(filepath.Join(s.uploadsDir, name))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		os.Remove(filepath.Join(s.uploadsDir, name))
		http.Error(w, err.Error(), 500)
		return
	}

	ip := clientIP(r)
	s.ips[name] = ip
	s.saveIPs()
	log.Printf("uploaded: %s (%d bytes) from %s", name, written, ip)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "name": name, "size": written})
}

func (s *server) resolveFile(name string) (string, error) {
	// Check uploads first, then assets
	path := filepath.Join(s.uploadsDir, name)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, nil
	}
	path = filepath.Join(s.assetsDir, name)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, nil
	}
	return "", fmt.Errorf("not found")
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Path)
	path, err := s.resolveFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.incDownload(name)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	http.ServeFile(w, r, path)
}

func main() {
	assetsDir := "assets"
	uploadsDir := "uploads"
	listen := ":8813"

	if d := os.Getenv("ASSETS_DIR"); d != "" {
		assetsDir = d
	}
	if d := os.Getenv("UPLOADS_DIR"); d != "" {
		uploadsDir = d
	}
	if l := os.Getenv("LISTEN"); l != "" {
		listen = l
	}

	os.MkdirAll(assetsDir, 0755)
	os.MkdirAll(uploadsDir, 0755)

	countsFile := filepath.Join(assetsDir, ".downloads.json")
	if c := os.Getenv("COUNTS_FILE"); c != "" {
		countsFile = c
	}
	ipsFile := filepath.Join(assetsDir, ".ips.json")
	if c := os.Getenv("IPS_FILE"); c != "" {
		ipsFile = c
	}
	s := &server{assetsDir: assetsDir, uploadsDir: uploadsDir, countsFile: countsFile, ipsFile: ipsFile}
	s.loadCounts()
	s.loadIPs()

	indexHTML, _ := indexFS.ReadFile("index.html")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	http.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files/" || r.URL.Path == "/files" {
			s.handleList(w, r)
			return
		}
		s.handleDownload(w, r)
	})
	http.HandleFunc("/upload", s.handleUpload)
	http.HandleFunc("/stats", s.handleStats)

	uTotal, _ := uploadsSize(uploadsDir)
	aFiles, _ := listDir(assetsDir, true)
	var aTotal int64
	for _, f := range aFiles {
		aTotal += f.Size
	}
	log.Printf("starting on %s, assets=%s (%d MB permanent), uploads=%s (%d MB / %d MB temp)",
		listen, assetsDir, aTotal>>20, uploadsDir, uTotal>>20, maxUploadBytes>>20)
	log.Fatal(http.ListenAndServe(listen, nil))
}
