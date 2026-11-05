package fingerprint

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/terminal/lobreplay"
)

// Footprint kinds. They name a pattern, not a participant: the tape is
// anonymous, so every detection is probabilistic.
const (
	KindTWAP              = "twap"
	KindVWAP              = "vwap"
	KindPOV               = "pov"
	KindIceberg           = "iceberg"
	KindMarginLiquidation = "margin_liquidation"
	KindUnknownAlgo       = "unknown_algo"
)

// Footprint is one detected execution pattern over one analysis window. Kind is
// one of the Kind* constants, Side is the aggressor side of the pattern
// (lobreplay.SideBuy/SideSell; empty for an unknown-algo cluster), Confidence is
// in [0,1] and Evidence carries the feature values the confidence was derived
// from, so a human can audit the attribution.
type Footprint struct {
	Kind       string             `json:"kind"`
	Symbol     string             `json:"symbol,omitempty"`
	Side       string             `json:"side,omitempty"`
	From       time.Time          `json:"from"`
	To         time.Time          `json:"to"`
	Confidence float64            `json:"confidence"`
	Evidence   map[string]float64 `json:"evidence,omitempty"`
}

// Detection defaults. The zero Config is valid: every zero field falls back to
// these values (see Config.withDefaults).
const (
	defaultWindow          = 60 * time.Second
	defaultStep            = 60 * time.Second
	defaultMinTrades       = 30
	defaultMinConfidence   = 0.6
	defaultMinClips        = 5
	defaultBinSize         = 250 * time.Millisecond
	defaultMinPeriod       = 300 * time.Millisecond
	defaultMaxPeriod       = 30 * time.Second
	defaultRefillWindow    = 2 * time.Second
	defaultVWAPBuckets     = 12
	defaultMinVWAPTrades   = 80
	defaultMinPOVTrades    = 80
	defaultDBSCANEps       = 0.45
	defaultDBSCANMinPoints = 5
)

// DefaultMarginCallWindows are the inferred broker margin-call windows in Moscow
// time (the plan Task 17 default, status I): forced close-outs cluster in the
// early afternoon and in the evening session, but the exact windows must be
// confirmed on a real recording with broker/risk reports. They are a hypothesis,
// not a fact.
var DefaultMarginCallWindows = []string{"14:00-14:30", "18:00-18:30"}

// Config configures Detect. All fields are optional; the zero value of a field
// selects the documented default. MarginCallWindows is the exception: nil selects
// DefaultMarginCallWindows while an explicitly empty slice disables the
// margin-call detector.
type Config struct {
	// Symbol is copied into every Footprint; it is not used by the detectors.
	Symbol string
	// Window is the analysis window length and Step its stride: Step == Window
	// means non-overlapping windows.
	Window time.Duration
	Step   time.Duration
	// MinTrades is the smallest number of trades a window must contain to be
	// analysed at all.
	MinTrades int
	// MinConfidence is the reporting threshold: a footprint below it is dropped.
	MinConfidence float64
	// MinClips is the smallest number of equal-size trades that can form a
	// TWAP/VWAP/POV clip series.
	MinClips int
	// BinSize is the resolution of the clip presence series of the periodicity
	// features.
	BinSize time.Duration
	// MinPeriod/MaxPeriod bound the searched clip period (the schedule interval).
	MinPeriod time.Duration
	MaxPeriod time.Duration
	// RefillWindow is the longest delay between the decrease of a level and its
	// iceberg refill.
	RefillWindow time.Duration
	// MarginCallWindows are "HH:MM-HH:MM" Moscow-time windows of expected
	// broker margin calls.
	MarginCallWindows []string
	// VWAPBuckets is the number of sub-buckets used by the VWAP correlation and
	// the POV share stability.
	VWAPBuckets int
	// MinVWAPTrades/MinPOVTrades gate the pace/profile detectors: a handful of
	// trades is never evidence of a schedule.
	MinVWAPTrades int
	MinPOVTrades  int
	// DBSCANEps/DBSCANMinPoints configure the unknown-algorithm clustering on the
	// standardized window features.
	DBSCANEps       float64
	DBSCANMinPoints int
}

// DefaultConfig returns the documented defaults.
func DefaultConfig() Config {
	return Config{
		Window:            defaultWindow,
		Step:              defaultStep,
		MinTrades:         defaultMinTrades,
		MinConfidence:     defaultMinConfidence,
		MinClips:          defaultMinClips,
		BinSize:           defaultBinSize,
		MinPeriod:         defaultMinPeriod,
		MaxPeriod:         defaultMaxPeriod,
		RefillWindow:      defaultRefillWindow,
		MarginCallWindows: append([]string(nil), DefaultMarginCallWindows...),
		VWAPBuckets:       defaultVWAPBuckets,
		MinVWAPTrades:     defaultMinVWAPTrades,
		MinPOVTrades:      defaultMinPOVTrades,
		DBSCANEps:         defaultDBSCANEps,
		DBSCANMinPoints:   defaultDBSCANMinPoints,
	}
}

// withDefaults fills the zero fields from DefaultConfig. A non-nil empty
// MarginCallWindows slice is kept: it disables the margin-call detector.
func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Window <= 0 {
		c.Window = d.Window
	}
	if c.Step <= 0 {
		c.Step = d.Step
	}
	if c.MinTrades <= 0 {
		c.MinTrades = d.MinTrades
	}
	if c.MinConfidence <= 0 {
		c.MinConfidence = d.MinConfidence
	}
	if c.MinClips <= 0 {
		c.MinClips = d.MinClips
	}
	if c.BinSize <= 0 {
		c.BinSize = d.BinSize
	}
	if c.MinPeriod <= 0 {
		c.MinPeriod = d.MinPeriod
	}
	if c.MaxPeriod <= 0 {
		c.MaxPeriod = d.MaxPeriod
	}
	if c.MaxPeriod < c.MinPeriod {
		c.MaxPeriod = c.MinPeriod
	}
	if c.RefillWindow <= 0 {
		c.RefillWindow = d.RefillWindow
	}
	if c.MarginCallWindows == nil {
		c.MarginCallWindows = append([]string(nil), DefaultMarginCallWindows...)
	}
	if c.VWAPBuckets < 3 {
		c.VWAPBuckets = d.VWAPBuckets
	}
	if c.MinVWAPTrades <= 0 {
		c.MinVWAPTrades = d.MinVWAPTrades
	}
	if c.MinPOVTrades <= 0 {
		c.MinPOVTrades = d.MinPOVTrades
	}
	if c.DBSCANEps <= 0 {
		c.DBSCANEps = d.DBSCANEps
	}
	if c.DBSCANMinPoints < 2 {
		c.DBSCANMinPoints = d.DBSCANMinPoints
	}
	return c
}

// Detect analyses the event tape and returns the footprints whose Confidence is
// at least Config.MinConfidence, ordered by From, Kind and Side. The tape is
// analysed in windows of Config.Window; TWAP/VWAP/POV are attributed per aggressor
// side, iceberg per book side, margin liquidation only inside the configured
// Moscow-time windows, and unknown_algo comes from a DBSCAN of the windows that
// no known detector explains.
//
// Detect is deterministic and reads no clock/network. A cancelled context stops
// the analysis early: the footprints found so far are returned (the plan fixes a
// no-error signature).
func Detect(ctx context.Context, events []lobreplay.Event, cfg Config) []Footprint {
	cfg = cfg.withDefaults()
	t := loadTape(events)
	if len(t.trades) == 0 {
		return nil
	}
	windows := splitWindows(t, cfg)
	margins := parseMarginCallWindows(cfg.MarginCallWindows)
	analyzed := make([]analyzedWindow, 0, len(windows))
	out := make([]Footprint, 0, 4)
	for _, w := range windows {
		if ctx != nil && ctx.Err() != nil {
			return sortFootprints(out)
		}
		if len(w.trades) < cfg.MinTrades {
			analyzed = append(analyzed, analyzedWindow{w: w})
			continue
		}
		features := computeWindowFeatures(w, cfg)
		strong := false
		for _, c := range classifyWindow(w, features, cfg, margins) {
			if c.confidence < cfg.MinConfidence {
				continue
			}
			strong = true
			out = append(out, footprintOf(c, w, cfg.Symbol))
		}
		analyzed = append(analyzed, analyzedWindow{w: w, f: features, hasFeatures: true, strong: strong})
	}
	out = append(out, unknownAlgoFootprints(ctx, analyzed, cfg)...)
	return sortFootprints(out)
}

// analyzedWindow is one window with its features and its known-detector verdict.
type analyzedWindow struct {
	w           window
	f           windowFeatures
	hasFeatures bool
	strong      bool
}

// candidate is one detector verdict before it becomes a Footprint.
type candidate struct {
	kind       string
	side       string
	confidence float64
	evidence   map[string]float64
}

// classifyWindow runs every detector over one window and returns the raw
// candidates; the reporting threshold is applied by Detect.
func classifyWindow(w window, f windowFeatures, cfg Config, margins []marginRange) []candidate {
	out := make([]candidate, 0, 4)
	out = append(out, twapCandidates(f, cfg)...)
	out = append(out, vwapCandidates(f, cfg)...)
	out = append(out, povCandidates(f, cfg)...)
	if c, ok := icebergCandidate(f); ok {
		out = append(out, c)
	}
	if overlapsMarginCall(w, margins) {
		if c, ok := marginLiquidationCandidate(f); ok {
			out = append(out, c)
		}
	}
	return out
}

// twapCandidates scores the equal-clip, evenly spaced schedule of both aggressor
// sides. The score multiplies the clip evidence (how many equal clips and how
// large is their share) by the timing regularity: a shuffled tape keeps the clip
// sizes but loses the schedule, so it cannot score high.
func twapCandidates(f windowFeatures, cfg Config) []candidate {
	sides := [2]string{lobreplay.SideBuy, lobreplay.SideSell}
	out := make([]candidate, 0, 2)
	for index, side := range sides {
		s := f.sides[index]
		if s.modeCount < cfg.MinClips || s.clipIntervals < cfg.MinClips {
			continue
		}
		// A window with a clear clip series still contains background trades on
		// the same side, so the share threshold is deliberately low: the
		// regularity factors below carry the discrimination.
		clipScore := clamp01((s.modeShare - 0.08) / 0.22)
		countFactor := clamp01(math.Log1p(float64(s.modeCount)) / math.Log(25))
		intervalScore := s.intervalRegularity
		autocorrScore := clamp01((s.autocorrPeak - 0.28) / 0.25)
		spectralScore := clamp01((s.spectralPeak - 0.02) / 0.05)
		regularity := 0.45*intervalScore + 0.30*autocorrScore + 0.25*spectralScore
		confidence := clipScore * countFactor * regularity
		if confidence <= 0 {
			continue
		}
		out = append(out, candidate{
			kind:       KindTWAP,
			side:       side,
			confidence: confidence,
			evidence: map[string]float64{
				"trades":              float64(s.trades),
				"volume":              float64(s.volume),
				"mode_qty":            float64(s.modeQty),
				"mode_share":          s.modeShare,
				"clip_count":          float64(s.modeCount),
				"interval_regularity": s.intervalRegularity,
				"interval_cv":         s.intervalCV,
				"median_interval_ms":  float64(s.medianInterval / time.Millisecond),
				"autocorr_peak":       s.autocorrPeak,
				"spectral_peak":       s.spectralPeak,
			},
		})
	}
	return out
}

// vwapCandidates scores the correlation of the trading pace with the modeled
// intraday volume profile. The volume gate keeps a handful of trades from being
// read as a schedule.
func vwapCandidates(f windowFeatures, cfg Config) []candidate {
	sides := [2]string{lobreplay.SideBuy, lobreplay.SideSell}
	out := make([]candidate, 0, 2)
	for index, side := range sides {
		s := f.sides[index]
		if s.trades < cfg.MinVWAPTrades {
			continue
		}
		corr := f.agg.vwapCorr[index]
		volumeFactor := clamp01(float64(s.trades) / float64(2*cfg.MinVWAPTrades))
		confidence := clamp01((corr-0.5)/0.35) * volumeFactor
		if confidence <= 0 {
			continue
		}
		out = append(out, candidate{
			kind:       KindVWAP,
			side:       side,
			confidence: confidence,
			evidence: map[string]float64{
				"trades":       float64(s.trades),
				"volume":       float64(s.volume),
				"profile_corr": corr,
			},
		})
	}
	return out
}

// povCandidates scores the stability of the side participation share over the
// window sub-buckets: a percentage-of-volume schedule keeps the share constant.
func povCandidates(f windowFeatures, cfg Config) []candidate {
	sides := [2]string{lobreplay.SideBuy, lobreplay.SideSell}
	out := make([]candidate, 0, 2)
	for index, side := range sides {
		s := f.sides[index]
		if s.trades < cfg.MinPOVTrades || !f.agg.povOk[index] {
			continue
		}
		mean, cv := f.agg.povMean[index], f.agg.povCV[index]
		stability := 1 - clamp01(cv/0.35)
		shareFactor := clamp01((mean - 0.02) / 0.08)
		volumeFactor := clamp01(float64(s.trades) / float64(2*cfg.MinPOVTrades))
		confidence := stability * shareFactor * volumeFactor
		if confidence <= 0 {
			continue
		}
		out = append(out, candidate{
			kind:       KindPOV,
			side:       side,
			confidence: confidence,
			evidence: map[string]float64{
				"trades":     float64(s.trades),
				"volume":     float64(s.volume),
				"share_mean": mean,
				"share_cv":   cv,
			},
		})
	}
	return out
}

// icebergCandidate turns the L2 refill cycles into a footprint.
func icebergCandidate(f windowFeatures) (candidate, bool) {
	iceberg := f.agg.iceberg
	if iceberg.cycles < 3 {
		return candidate{}, false
	}
	side := lobreplay.SideBuy
	if iceberg.side == lobreplay.SideAsk {
		side = lobreplay.SideSell
	}
	confidence := clamp01(0.5 + 0.1*float64(iceberg.cycles-3) + 0.02*float64(iceberg.prices))
	return candidate{
		kind:       KindIceberg,
		side:       side,
		confidence: confidence,
		evidence: map[string]float64{
			"refill_cycles": float64(iceberg.cycles),
			"refills":       float64(iceberg.refills),
			"prices":        float64(iceberg.prices),
			"cycle_size":    float64(iceberg.cycleSize),
			"price":         float64(iceberg.maxPrice) / float64(priceFactor),
		},
	}, true
}

// marginLiquidationCandidate scores a series of aggressive sells with growing
// size and a falling price. The caller already checked the Moscow-time window.
func marginLiquidationCandidate(f windowFeatures) (candidate, bool) {
	if f.sides[sideSell].trades < 3 {
		return candidate{}, false
	}
	sellShareScore := clamp01((f.agg.sellShare - 0.55) / 0.25)
	growthScore := clamp01((f.agg.sizeGrowth - 0.10) / 0.50)
	declineScore := clamp01((f.agg.priceDecline - 0.0005) / 0.0015)
	confidence := 0.30*sellShareScore + 0.35*growthScore + 0.35*declineScore
	if confidence <= 0 {
		return candidate{}, false
	}
	return candidate{
		kind:       KindMarginLiquidation,
		side:       lobreplay.SideSell,
		confidence: confidence,
		evidence: map[string]float64{
			"sell_share":    f.agg.sellShare,
			"size_growth":   f.agg.sizeGrowth,
			"price_decline": f.agg.priceDecline,
			"trades":        float64(f.agg.trades),
			"sell_trades":   float64(f.sides[sideSell].trades),
			"sell_volume":   float64(f.sides[sideSell].volume),
		},
	}, true
}

// marginRange is one configured margin-call window in minutes since midnight
// Moscow time. end <= start means the window crosses midnight.
type marginRange struct {
	start int
	end   int
}

// parseMarginCallWindows parses "HH:MM-HH:MM" specs, dropping malformed ones: a
// typo in an auxiliary time window must not disable the whole analysis.
func parseMarginCallWindows(specs []string) []marginRange {
	out := make([]marginRange, 0, len(specs))
	for _, spec := range specs {
		from, to, ok := strings.Cut(strings.TrimSpace(spec), "-")
		if !ok {
			continue
		}
		start, err := parseClock(from)
		if err != nil {
			continue
		}
		end, err := parseClock(to)
		if err != nil {
			continue
		}
		out = append(out, marginRange{start: start, end: end})
	}
	return out
}

// parseClock parses "HH:MM" into minutes since midnight.
func parseClock(value string) (int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

// moscow is UTC+3: Russian exchanges keep Moscow time without daylight saving, so
// a fixed zone is exact and needs no tzdata.
var moscow = time.FixedZone("MSK", 3*60*60)

// overlapsMarginCall reports whether the analysis window intersects one of the
// configured Moscow-time windows of the day the window starts on.
func overlapsMarginCall(w window, ranges []marginRange) bool {
	if len(ranges) == 0 {
		return false
	}
	local := w.from.In(moscow)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, moscow)
	for _, r := range ranges {
		start := day.Add(time.Duration(r.start) * time.Minute)
		end := day.Add(time.Duration(r.end) * time.Minute)
		if r.end <= r.start {
			end = end.Add(24 * time.Hour)
		}
		if w.from.Before(end) && w.to.After(start) {
			return true
		}
	}
	return false
}

// clusterVector is the standardized feature vector of an unexplained window.
func clusterVector(f windowFeatures) []float64 {
	return []float64{
		math.Log1p(float64(f.agg.trades)),
		f.agg.sellShare,
		f.sides[sideBuy].entropy,
		f.sides[sideSell].entropy,
		f.sides[sideBuy].modeShare,
		f.sides[sideSell].modeShare,
		f.sides[sideBuy].autocorrPeak,
		f.sides[sideSell].autocorrPeak,
		f.sides[sideBuy].intervalCV,
		f.sides[sideSell].intervalCV,
		f.agg.vwapCorr[sideBuy],
		f.agg.vwapCorr[sideSell],
		f.agg.priceDecline,
	}
}

// unknownAlgoFootprints clusters the windows that no known detector explains and
// turns each dense cluster into an unknown_algo footprint. The clustering needs
// at least two clusters' worth of unexplained windows: a handful of windows is
// never evidence.
func unknownAlgoFootprints(ctx context.Context, analyzed []analyzedWindow, cfg Config) []Footprint {
	points := make([][]float64, 0, len(analyzed))
	windowIndex := make([]int, 0, len(analyzed))
	for index, aw := range analyzed {
		if !aw.hasFeatures || aw.strong {
			continue
		}
		points = append(points, clusterVector(aw.f))
		windowIndex = append(windowIndex, index)
	}
	if len(points) < 2*cfg.DBSCANMinPoints {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	standardized := standardize(points)
	labels := dbscanLabels(standardized, cfg.DBSCANEps, cfg.DBSCANMinPoints)
	byCluster := make(map[int][]int)
	for position, label := range labels {
		if label >= 0 {
			byCluster[label] = append(byCluster[label], position)
		}
	}
	clusterIDs := make([]int, 0, len(byCluster))
	for id := range byCluster {
		clusterIDs = append(clusterIDs, id)
	}
	sort.Ints(clusterIDs)
	out := make([]Footprint, 0, len(clusterIDs))
	for _, id := range clusterIDs {
		members := byCluster[id]
		if len(members) < cfg.DBSCANMinPoints {
			continue
		}
		windows := make([]int, 0, len(members))
		for _, position := range members {
			windows = append(windows, windowIndex[position])
		}
		sort.Ints(windows)
		cohesion := clusterCohesion(standardized, members, cfg.DBSCANEps)
		sizeFactor := clamp01(float64(len(members)) / 8)
		confidence := clamp01(0.30 + 0.30*cohesion + 0.10*sizeFactor)
		if confidence < cfg.MinConfidence {
			continue
		}
		out = append(out, Footprint{
			Kind:       KindUnknownAlgo,
			Symbol:     cfg.Symbol,
			From:       analyzed[windows[0]].w.from,
			To:         analyzed[windows[len(windows)-1]].w.to,
			Confidence: round4(confidence),
			Evidence: map[string]float64{
				"cluster_size": float64(len(members)),
				"cohesion":     round4(cohesion),
			},
		})
	}
	return out
}

// footprintOf converts one candidate into the reported footprint.
func footprintOf(c candidate, w window, symbol string) Footprint {
	evidence := make(map[string]float64, len(c.evidence))
	for key, value := range c.evidence {
		evidence[key] = round4(value)
	}
	return Footprint{
		Kind:       c.kind,
		Symbol:     symbol,
		Side:       c.side,
		From:       w.from,
		To:         w.to,
		Confidence: round4(clamp01(c.confidence)),
		Evidence:   evidence,
	}
}

// sortFootprints orders the footprints deterministically.
func sortFootprints(footprints []Footprint) []Footprint {
	sort.SliceStable(footprints, func(i, j int) bool {
		a, b := footprints[i], footprints[j]
		if !a.From.Equal(b.From) {
			return a.From.Before(b.From)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Side < b.Side
	})
	return footprints
}

// clamp01 clamps a score into [0,1].
func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// round4 rounds a metric to four decimals: enough for a confident score, small
// enough to keep the JSON payload stable across runs.
func round4(value float64) float64 {
	return math.Round(value*1e4) / 1e4
}
