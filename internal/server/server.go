// Package server expõe a API HTTP e o dashboard estático do job-scout.
package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"job-scout/internal/scheduler"
	"job-scout/internal/storage"
)

//go:embed static
var staticFiles embed.FS

const resumeDir = "data/resumes"

var validStatuses = map[string]bool{
	"new":      true,
	"reviewed": true,
	"applied":  true,
	"ignored":  true,
}

// Server expõe a API HTTP e o dashboard do job-scout.
type Server struct {
	db        *storage.DB
	scheduler *scheduler.Scheduler
	mux       *http.ServeMux
}

// New cria um Server com as rotas da API e os arquivos estáticos do
// dashboard já registrados.
func New(db *storage.DB, sched *scheduler.Scheduler) *Server {
	s := &Server{db: db, scheduler: sched, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler retorna o http.Handler completo do servidor.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(static)))

	s.mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	s.mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	s.mux.HandleFunc("GET /api/jobs/{id}/resume", s.handleGetResume)
	s.mux.HandleFunc("POST /api/jobs/{id}/status", s.handleUpdateStatus)
	s.mux.HandleFunc("POST /api/trigger", s.handleTrigger)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !validStatuses[status] {
		writeError(w, http.StatusBadRequest, "status inválido")
		return
	}

	minScore := 0
	if raw := r.URL.Query().Get("min_score"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "min_score inválido")
			return
		}
		minScore = parsed
	}

	jobs, err := s.db.GetJobsWithAnalysis(status, minScore)
	if err != nil {
		slog.Error("server: erro ao buscar vagas", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao buscar vagas")
		return
	}

	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	job, analysis, err := s.db.GetJobByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "vaga não encontrada")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job":      job,
		"analysis": analysis,
	})
}

func (s *Server) handleGetResume(w http.ResponseWriter, r *http.Request) {
	id := sanitizeID(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}

	pdfPath := filepath.Join(resumeDir, id+".pdf")
	if _, err := os.Stat(pdfPath); err == nil {
		http.ServeFile(w, r, pdfPath)
		return
	}

	mdPath := filepath.Join(resumeDir, id+".md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "currículo não encontrado")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.Warn("server: erro ao enviar currículo em markdown", "id", id, "error", err)
	}
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "corpo da requisição inválido")
		return
	}
	if !validStatuses[body.Status] {
		writeError(w, http.StatusBadRequest, "status inválido")
		return
	}

	if err := s.db.UpdateJobStatus(id, body.Status); err != nil {
		writeError(w, http.StatusNotFound, "vaga não encontrada")
		return
	}

	job, _, err := s.db.GetJobByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "erro ao buscar vaga atualizada")
		return
	}

	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	go s.scheduler.RunNow()
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "pipeline iniciado"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	counts, err := s.db.CountJobsByStatus()
	if err != nil {
		slog.Error("server: erro ao contar vagas por status", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao buscar status")
		return
	}

	resp := map[string]any{
		"counts_by_status": counts,
		"last_run":         nil,
		"next_run":         nil,
	}

	if last, ok := s.scheduler.LastRun(); ok {
		resp["last_run"] = last.Format(time.RFC3339)
	}
	if next, err := s.scheduler.NextRun(); err == nil {
		resp["next_run"] = next.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("server: erro ao serializar resposta json", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// sanitizeID reduz o id ao seu nome base, prevenindo path traversal ao
// montar caminhos de arquivo a partir de um valor vindo da URL.
func sanitizeID(id string) string {
	base := filepath.Base(id)
	if base == "." || base == string(filepath.Separator) || strings.ContainsAny(id, `/\`) {
		return ""
	}
	return base
}
