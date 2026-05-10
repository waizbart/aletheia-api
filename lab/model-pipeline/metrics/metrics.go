// Package metrics fornece coleta de métricas de desempenho (tempo, memória, CPU)
// para o pipeline perceptual. Usa runtime.ReadMemStats para heap Go e
// /proc/self/status + /proc/self/stat para métricas reais do processo (RSS, CPU time).
//
// O RSS via /proc é essencial porque o ONNX Runtime roda via CGO e sua memória
// não aparece no runtime.GoMemStats.
package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Structs
// ---------------------------------------------------------------------------

// StageMetrics contém as métricas coletadas para uma etapa da pipeline.
type StageMetrics struct {
	Duration    time.Duration `json:"duration_ns"`
	DeltaHeapMB float64       `json:"delta_heap_mb"`
	RSSMB       float64       `json:"rss_mb"`
	UserCPUS    float64       `json:"user_cpu_s"`
	SysCPUS     float64       `json:"sys_cpu_s"`
	DurationMS  float64       `json:"duration_ms"` // derivado
}

// ProcSnapshot é uma fotografia do processo num instante.
type ProcSnapshot struct {
	RSSBytes  int64
	UserTicks int64
	SysTicks  int64
	MemStats  runtime.MemStats
}

// PerImageMetrics agrega as métricas de uma imagem candidata.
type PerImageMetrics struct {
	ImageName string                  `json:"image_name"`
	Authentic bool                    `json:"authentic"`
	PathType  string                  `json:"path_type"`
	Stages    map[string]StageMetrics `json:"stages"`
	Total     StageMetrics            `json:"total"`
}

// ReportSummary contém as médias e totais consolidados.
type ReportSummary struct {
	TotalImages   int                     `json:"total_images"`
	AvgDurationMS float64                 `json:"avg_duration_ms"`
	AvgHeapMB     float64                 `json:"avg_heap_mb"`
	AvgRSSMB      float64                 `json:"avg_rss_mb"`
	AvgUserCPUS   float64                 `json:"avg_user_cpu_s"`
	AvgSysCPUS    float64                 `json:"avg_sys_cpu_s"`
	StageAverages map[string]StageMetrics `json:"stage_averages,omitempty"`
}

// Report contém o relatório completo de desempenho.
type Report struct {
	PerImage []PerImageMetrics `json:"per_image"`
	Summary  ReportSummary     `json:"summary"`
}

// ---------------------------------------------------------------------------
// Coleta de snapshots
// ---------------------------------------------------------------------------

// Snapshot captura o estado atual do processo (RSS, CPU ticks, memória Go).
func Snapshot() ProcSnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return ProcSnapshot{
		RSSBytes:  readRSS(),
		UserTicks: readCPUTime(utimeField),
		SysTicks:  readCPUTime(stimeField),
		MemStats:  m,
	}
}

// Diff calcula StageMetrics entre dois snapshots.
func (before ProcSnapshot) Diff(after ProcSnapshot, dur time.Duration) StageMetrics {
	deltaHeap := float64(after.MemStats.TotalAlloc-before.MemStats.TotalAlloc) / (1024 * 1024)
	userSec := float64(after.UserTicks-before.UserTicks) / float64(clockTicksPerSec)
	sysSec := float64(after.SysTicks-before.SysTicks) / float64(clockTicksPerSec)
	durMS := dur.Seconds() * 1000

	return StageMetrics{
		Duration:    dur,
		DeltaHeapMB: deltaHeap,
		RSSMB:       float64(after.RSSBytes) / (1024 * 1024),
		UserCPUS:    userSec,
		SysCPUS:     sysSec,
		DurationMS:  durMS,
	}
}

// ---------------------------------------------------------------------------
// Leitura de /proc (Linux)
// ---------------------------------------------------------------------------

// Índices dos campos em /proc/self/stat (1-based, POSIX).
const (
	utimeField = 14
	stimeField = 15
)

// readRSS lê o VmRSS de /proc/self/status (resident set size em bytes).
func readRSS() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				kb, err := strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					return 0
				}
				return kb * 1024
			}
		}
	}
	return 0
}

// readCPUTime lê um campo numérico de /proc/self/stat.
// Formato: pid (comm) state ppid ... utime stime ...
// O (comm) pode conter espaços, então busca-se o último ')'.
func readCPUTime(fieldIdx int) int64 {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(data))
	closeParen := strings.LastIndex(s, ")")
	if closeParen < 0 {
		return 0
	}
	after := s[closeParen+2:]
	fields := strings.Fields(after)
	if len(fields) >= fieldIdx {
		val, err := strconv.ParseInt(fields[fieldIdx-1], 10, 64)
		if err != nil {
			return 0
		}
		return val
	}
	return 0
}

// clockTicksPerSec é o número de ticks por segundo (sysconf _SC_CLK_TCK).
// Em praticamente todos os Linux modernos é 100.
const clockTicksPerSec = 100

// DiffTotal calcula StageMetrics agregadas a partir de métricas de sub-etapas.
// Duração e heap são somados; RSS é o pico entre as etapas; CPU é somado.
func DiffTotal(stages map[string]StageMetrics) StageMetrics {
	var totalDur time.Duration
	var totalHeap, totalRSS, totalUser, totalSys float64

	for _, s := range stages {
		totalDur += s.Duration
		totalHeap += s.DeltaHeapMB
		if s.RSSMB > totalRSS {
			totalRSS = s.RSSMB
		}
		totalUser += s.UserCPUS
		totalSys += s.SysCPUS
	}

	return StageMetrics{
		Duration:    totalDur,
		DeltaHeapMB: totalHeap,
		RSSMB:       totalRSS,
		UserCPUS:    totalUser,
		SysCPUS:     totalSys,
		DurationMS:  totalDur.Seconds() * 1000,
	}
}

// ---------------------------------------------------------------------------
// Geração de relatório
// ---------------------------------------------------------------------------

// NewReport gera um Report a partir das métricas por imagem.
func NewReport(perImage []PerImageMetrics) Report {
	r := Report{PerImage: perImage}

	var totalDur, totalHeap, totalRSS, totalUser, totalSys float64

	type stageAgg struct {
		count                                              int
		totalDur, totalHeap, totalRSS, totalUser, totalSys float64
	}
	stageAggs := make(map[string]*stageAgg)

	for _, pi := range perImage {
		totalDur += pi.Total.DurationMS
		totalHeap += pi.Total.DeltaHeapMB
		totalRSS += pi.Total.RSSMB
		totalUser += pi.Total.UserCPUS
		totalSys += pi.Total.SysCPUS

		for stage, sm := range pi.Stages {
			a, ok := stageAggs[stage]
			if !ok {
				a = &stageAgg{}
				stageAggs[stage] = a
			}
			a.count++
			a.totalDur += sm.DurationMS
			a.totalHeap += sm.DeltaHeapMB
			a.totalRSS += sm.RSSMB
			a.totalUser += sm.UserCPUS
			a.totalSys += sm.SysCPUS
		}
	}

	n := len(perImage)
	if n == 0 {
		n = 1
	}

	r.Summary = ReportSummary{
		TotalImages:   len(perImage),
		AvgDurationMS: totalDur / float64(n),
		AvgHeapMB:     totalHeap / float64(n),
		AvgRSSMB:      totalRSS / float64(n),
		AvgUserCPUS:   totalUser / float64(n),
		AvgSysCPUS:    totalSys / float64(n),
		StageAverages: make(map[string]StageMetrics),
	}

	for stage, a := range stageAggs {
		if a.count == 0 {
			continue
		}
		avgDurMS := a.totalDur / float64(a.count)
		r.Summary.StageAverages[stage] = StageMetrics{
			Duration:    time.Duration(avgDurMS * float64(time.Millisecond)),
			DeltaHeapMB: a.totalHeap / float64(a.count),
			RSSMB:       a.totalRSS / float64(a.count),
			UserCPUS:    a.totalUser / float64(a.count),
			SysCPUS:     a.totalSys / float64(a.count),
			DurationMS:  avgDurMS,
		}
	}

	return r
}

// WriteJSON serializa o relatório como JSON indentado.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText escreve o relatório em formato tabular legível.
func (r Report) WriteText(w io.Writer) {
	sep := strings.Repeat("─", 98)

	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "┌%s┐\n", sep)
	fmt.Fprintf(w, "│  %-92s  │\n", "RELATÓRIO DE DESEMPENHO")
	fmt.Fprintf(w, "├%s┤\n", sep)
	fmt.Fprintf(w, "│  %-92s  │\n", fmt.Sprintf("Total de imagens: %d", r.Summary.TotalImages))
	fmt.Fprintf(w, "└%s┘\n", sep)
	fmt.Fprintf(w, "\n")

	// Médias por estágio
	fmt.Fprintf(w, "Médias por Estágio:\n")
	fmt.Fprintf(w, "  %-14s │ %12s │ %10s │ %10s │ %11s │ %11s\n",
		"Estágio", "Tempo (ms)", "Heap Go", "RSS", "CPU User", "CPU Sys")
	fmt.Fprintf(w, "  "+strings.Repeat("─", 85)+"\n")

	// Ordem dos estágios
	stageOrder := []string{"L0 (RotNet)", "L1 (pHash)", "L2 (DINOv2)", "L4 (Cores)"}
	for _, name := range stageOrder {
		sm, ok := r.Summary.StageAverages[name]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "  %-14s │ %12.3f │ %9.2fMB │ %9.2fMB │ %10.4fs │ %10.4fs\n",
			name, sm.DurationMS, sm.DeltaHeapMB, sm.RSSMB, sm.UserCPUS, sm.SysCPUS)
	}

	// Total (média geral)
	fmt.Fprintf(w, "  "+strings.Repeat("─", 85)+"\n")
	fmt.Fprintf(w, "  %-14s │ %12.3f │ %9.2fMB │ %9.2fMB │ %10.4fs │ %10.4fs\n",
		"Total (média)", r.Summary.AvgDurationMS, r.Summary.AvgHeapMB,
		r.Summary.AvgRSSMB, r.Summary.AvgUserCPUS, r.Summary.AvgSysCPUS)
	fmt.Fprintf(w, "\n")

	// Por imagem
	fmt.Fprintf(w, "Por Imagem:\n")
	fmt.Fprintf(w, "  %-24s │ %-9s │ %-8s │ %12s │ %10s │ %10s\n",
		"Imagem", "Resultado", "Caminho", "Tempo (ms)", "Heap Go", "RSS")
	fmt.Fprintf(w, "  "+strings.Repeat("─", 85)+"\n")

	for _, pi := range r.PerImage {
		resultLabel := "ADULTERADA"
		if pi.Authentic {
			resultLabel = "AUTÊNTICA"
		}
		fmt.Fprintf(w, "  %-24s │ %-9s │ %-8s │ %12.3f │ %9.2fMB │ %9.2fMB\n",
			pi.ImageName, resultLabel, pi.PathType,
			pi.Total.DurationMS, pi.Total.DeltaHeapMB, pi.Total.RSSMB)

		// Detalhamento por estágio nesta imagem
		for _, sname := range stageOrder {
			sm, ok := pi.Stages[sname]
			if !ok {
				continue
			}
			fmt.Fprintf(w, "    └─ %-14s │ %12.3f │ %9.2fMB │ %9.2fMB │ %10.4fs │ %10.4fs\n",
				sname, sm.DurationMS, sm.DeltaHeapMB, sm.RSSMB, sm.UserCPUS, sm.SysCPUS)
		}
	}
	fmt.Fprintf(w, "\n")
}
