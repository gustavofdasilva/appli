package models

import "time"

// Job representa uma vaga encontrada por um crawler.
type Job struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Title       string    `json:"title"`
	Company     string    `json:"company"`
	Location    string    `json:"location"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	Salary      string    `json:"salary"`
	FoundAt     time.Time `json:"found_at"`
	Status      string    `json:"status"`
}

// Analysis representa o resultado da análise de fit de uma vaga feita pela LLM.
type Analysis struct {
	ID            string    `json:"id"`
	JobID         string    `json:"job_id"`
	FitScore      int       `json:"fit_score"`
	Summary       string    `json:"summary"`
	Benefits      string    `json:"benefits"`
	FitReasoning  string    `json:"fit_reasoning"`
	ResumeMD      string    `json:"resume_md"`
	ResumePDFPath string    `json:"resume_pdf_path"`
	CreatedAt     time.Time `json:"created_at"`
}

// Profile representa um par chave/valor do perfil do usuário armazenado no banco.
type Profile struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
