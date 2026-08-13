package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"job-scout/internal/models"
)

// DB encapsula a conexão com o banco SQLite e as operações do job-scout.
type DB struct {
	conn *sql.DB
}

// Open conecta ao banco SQLite no caminho informado e garante que as tabelas existam.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("erro ao abrir banco de dados %q: %w", path, err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("erro ao conectar ao banco de dados %q: %w", path, err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

// Close fecha a conexão com o banco de dados.
func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL,
	title TEXT NOT NULL,
	company TEXT NOT NULL,
	location TEXT,
	url TEXT NOT NULL UNIQUE,
	description TEXT,
	salary TEXT,
	found_at DATETIME NOT NULL,
	status TEXT NOT NULL DEFAULT 'new'
);

CREATE TABLE IF NOT EXISTS analyses (
	id TEXT PRIMARY KEY,
	job_id TEXT NOT NULL REFERENCES jobs(id),
	fit_score INTEGER NOT NULL,
	summary TEXT,
	benefits TEXT,
	fit_reasoning TEXT,
	resume_md TEXT,
	resume_pdf_path TEXT,
	created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS profile (
	key TEXT PRIMARY KEY,
	value TEXT
);
`
	if _, err := db.conn.Exec(schema); err != nil {
		return fmt.Errorf("erro ao criar tabelas: %w", err)
	}
	return nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// InsertJob insere uma nova vaga no banco. Se ID, FoundAt ou Status estiverem
// vazios, valores padrão são gerados automaticamente.
func (db *DB) InsertJob(job models.Job) error {
	if job.ID == "" {
		id, err := newID()
		if err != nil {
			return fmt.Errorf("erro ao gerar id da vaga: %w", err)
		}
		job.ID = id
	}
	if job.FoundAt.IsZero() {
		job.FoundAt = time.Now()
	}
	if job.Status == "" {
		job.Status = "new"
	}

	_, err := db.conn.Exec(
		`INSERT INTO jobs (id, source, title, company, location, url, description, salary, found_at, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Source, job.Title, job.Company, job.Location, job.URL, job.Description, job.Salary, job.FoundAt, job.Status,
	)
	if err != nil {
		return fmt.Errorf("erro ao inserir vaga: %w", err)
	}
	return nil
}

// JobExistsByURL verifica se já existe uma vaga cadastrada com a URL informada.
func (db *DB) JobExistsByURL(url string) bool {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(1) FROM jobs WHERE url = ?`, url).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// GetJobs retorna as vagas filtradas por status e/ou pontuação mínima de fit.
// Strings vazias e valores <= 0 desativam o respectivo filtro.
func (db *DB) GetJobs(status string, minScore int) ([]models.Job, error) {
	query := `
		SELECT DISTINCT j.id, j.source, j.title, j.company, j.location, j.url, j.description, j.salary, j.found_at, j.status
		FROM jobs j
		LEFT JOIN analyses a ON a.job_id = j.id
		WHERE 1=1
	`
	args := []any{}

	if status != "" {
		query += " AND j.status = ?"
		args = append(args, status)
	}
	if minScore > 0 {
		query += " AND a.fit_score >= ?"
		args = append(args, minScore)
	}
	query += " ORDER BY j.found_at DESC"

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar vagas: %w", err)
	}
	defer rows.Close()

	var jobs []models.Job
	for rows.Next() {
		var j models.Job
		if err := rows.Scan(&j.ID, &j.Source, &j.Title, &j.Company, &j.Location, &j.URL, &j.Description, &j.Salary, &j.FoundAt, &j.Status); err != nil {
			return nil, fmt.Errorf("erro ao ler vaga: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar vagas: %w", err)
	}

	return jobs, nil
}

// GetJobByID retorna a vaga e sua análise mais recente (se existir).
func (db *DB) GetJobByID(id string) (models.Job, models.Analysis, error) {
	var job models.Job
	err := db.conn.QueryRow(
		`SELECT id, source, title, company, location, url, description, salary, found_at, status FROM jobs WHERE id = ?`,
		id,
	).Scan(&job.ID, &job.Source, &job.Title, &job.Company, &job.Location, &job.URL, &job.Description, &job.Salary, &job.FoundAt, &job.Status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Job{}, models.Analysis{}, fmt.Errorf("vaga %q não encontrada: %w", id, err)
		}
		return models.Job{}, models.Analysis{}, fmt.Errorf("erro ao buscar vaga %q: %w", id, err)
	}

	var analysis models.Analysis
	err = db.conn.QueryRow(
		`SELECT id, job_id, fit_score, summary, benefits, fit_reasoning, resume_md, resume_pdf_path, created_at
		 FROM analyses WHERE job_id = ? ORDER BY created_at DESC LIMIT 1`,
		id,
	).Scan(&analysis.ID, &analysis.JobID, &analysis.FitScore, &analysis.Summary, &analysis.Benefits, &analysis.FitReasoning, &analysis.ResumeMD, &analysis.ResumePDFPath, &analysis.CreatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return job, models.Analysis{}, fmt.Errorf("erro ao buscar análise da vaga %q: %w", id, err)
	}

	return job, analysis, nil
}

// InsertAnalysis insere uma nova análise no banco e retorna o ID usado. Se ID
// ou CreatedAt estiverem vazios, valores padrão são gerados automaticamente.
func (db *DB) InsertAnalysis(a models.Analysis) (string, error) {
	if a.ID == "" {
		id, err := newID()
		if err != nil {
			return "", fmt.Errorf("erro ao gerar id da análise: %w", err)
		}
		a.ID = id
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}

	_, err := db.conn.Exec(
		`INSERT INTO analyses (id, job_id, fit_score, summary, benefits, fit_reasoning, resume_md, resume_pdf_path, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.JobID, a.FitScore, a.Summary, a.Benefits, a.FitReasoning, a.ResumeMD, a.ResumePDFPath, a.CreatedAt,
	)
	if err != nil {
		return "", fmt.Errorf("erro ao inserir análise: %w", err)
	}
	return a.ID, nil
}

// UpdateJobStatus atualiza o status de uma vaga existente.
func (db *DB) UpdateJobStatus(id string, status string) error {
	res, err := db.conn.Exec(`UPDATE jobs SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("erro ao atualizar status da vaga %q: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("erro ao verificar atualização da vaga %q: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("vaga %q não encontrada", id)
	}
	return nil
}

// UpdateResumePath atualiza o caminho do PDF do currículo gerado para uma análise.
func (db *DB) UpdateResumePath(analysisID string, path string) error {
	res, err := db.conn.Exec(`UPDATE analyses SET resume_pdf_path = ? WHERE id = ?`, path, analysisID)
	if err != nil {
		return fmt.Errorf("erro ao atualizar caminho do currículo da análise %q: %w", analysisID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("erro ao verificar atualização da análise %q: %w", analysisID, err)
	}
	if rows == 0 {
		return fmt.Errorf("análise %q não encontrada", analysisID)
	}
	return nil
}
