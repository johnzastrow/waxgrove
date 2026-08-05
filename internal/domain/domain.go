// Package domain holds the core types shared across Waxgrove. It depends on
// nothing else in the project, so the repository, HTTP and connector layers can
// all speak the same language without importing each other.
package domain

import "time"

// Tier distinguishes records the group deliberately added from records that
// arrived as a side effect of an album fetch (D11).
type Tier string

const (
	// TierCurated records were deliberately added and appear in search.
	TierCurated Tier = "curated"
	// TierAmbient records exist only to make resolution instant. They stay out
	// of search until first deliberate use, which promotes them (F24).
	TierAmbient Tier = "ambient"
)

// Record is a canonical song. Identity is the MBID; ISRCs are a many-to-one
// lookup key, so this holds a set of them rather than one (BR-1).
type Record struct {
	ID           string
	MBID         string // empty until MusicBrainz matches
	Title        string
	ArtistCredit string
	Album        string
	DurationMS   int
	Year         int
	ISRCs        []string
	NormTitle    string
	NormArtist   string
	Tier         Tier
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// MatchMethod records how a record was resolved, so a bad match is auditable
// and re-resolvable later (§3.2).
type MatchMethod string

const (
	MatchISRC   MatchMethod = "isrc"   // exact, automatic
	MatchMBID   MatchMethod = "mbid"   // exact, automatic
	MatchMapper MatchMethod = "mapper" // ListenBrainz MBID Mapper
	MatchFuzzy  MatchMethod = "fuzzy"  // local normalised comparison
	MatchNone   MatchMethod = ""       // unresolved
)

// Match is the outcome of running a candidate through the resolution ladder.
type Match struct {
	Record     *Record
	Method     MatchMethod
	Confidence float64
	// Alternatives are populated when confidence is below threshold, so the
	// disambiguation UI can offer a choice rather than Waxgrove guessing.
	Alternatives []Candidate
}

// Resolved reports whether the match may be applied without asking a human.
func (m Match) Resolved() bool {
	return m.Record != nil && m.Confidence >= ConfidenceThreshold
}

// ConfidenceThreshold is the line below which §3.2 requires disambiguation
// rather than a guess.
const ConfidenceThreshold = 0.85

// Candidate is an unresolved song reference from any source adapter (§3.3).
// Everything a source can offer is optional except that something identifies it.
//
// The JSON tags are part of the public API: candidates travel over the wire in
// both directions (posted to add tracks, returned as unresolved items and as
// disambiguation alternatives), so they use the same snake_case shape as every
// other view rather than defaulting to Go field names.
type Candidate struct {
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
	ISRC       string `json:"isrc,omitempty"`
	MBID       string `json:"mbid,omitempty"`
	Year       int    `json:"year,omitempty"`
	// Raw is the original text for free-text sources, preserved so an
	// unresolved item is never silently dropped (BR-5).
	Raw string `json:"raw,omitempty"`
	// SourceRef names the adapter that produced this candidate.
	SourceRef string `json:"source_ref,omitempty"`
}

// User is an instance member.
type User struct {
	ID           string
	Email        string // empty once anonymised (BR-4)
	DisplayName  string
	Role         string
	HasPassword  bool
	OIDCSubject  string
	CreatedAt    time.Time
	DeletedAt    *time.Time
	AnonymizedAt *time.Time
}

const (
	RoleMember = "member"
	RoleAdmin  = "admin"
)

// Playlist is an ordered list of canonical records owned by one user and
// shared by reference (D8).
type Playlist struct {
	ID          string
	OwnerID     string // empty once the owner is anonymised
	Title       string
	Description string
	CurrentRev  int
	Tracks      []Track
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Track is one position in a playlist.
type Track struct {
	Position   int
	Record     Record
	AddedInRev int
}

// RevisionOp names a content change. Annotations never produce one (BR-3).
type RevisionOp string

const (
	OpCreate  RevisionOp = "create"
	OpAdd     RevisionOp = "add"
	OpRemove  RevisionOp = "remove"
	OpReorder RevisionOp = "reorder"
	OpRename  RevisionOp = "rename"
)

// JobState tracks a long provider operation (F22).
type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobPaused    JobState = "paused"
	JobDone      JobState = "done"
	JobFailed    JobState = "failed"
	JobCancelled JobState = "cancelled"
)

// Job kinds.
const (
	JobImport = "import"
	JobExport = "export"
)

// Job is a resumable provider operation with visible progress.
//
// Provider work is a job rather than a request because a cold export can exceed
// a minute (§7), and because a self-hosted box restarts for updates — progress
// that lives only in a goroutine is progress the user loses.
type Job struct {
	ID         string
	Kind       string
	State      JobState
	UserID     string
	PlaylistID string
	Service    string
	Storefront string
	// SourceRef is what the user pasted, for an import. Persisted so a job
	// requeued after a restart still knows what it was importing.
	SourceRef string
	Done      int
	Total     int
	Error     string
	CreatedAt string
	UpdatedAt string
	Items     []JobItem
}

// Terminal reports whether a job has finished, however it finished.
func (j Job) Terminal() bool {
	switch j.State {
	case JobDone, JobFailed, JobCancelled:
		return true
	}
	return false
}

// Job item outcomes. Everything except JobItemOK is a track the user needs to
// know about (F15).
const (
	JobItemOK          = "ok"
	JobItemUnavailable = "unavailable" // not on that service in that storefront
	JobItemUnresolved  = "unresolved"  // Waxgrove could not identify it at all
	JobItemFailed      = "failed"
)

// JobItem is the outcome for one track. Failures are recorded, never dropped:
// partial success is the normal result of an export, and a quietly shorter
// playlist is the worst way to deliver it (F15).
type JobItem struct {
	Position int
	RecordID string
	Status   string
	Detail   string
}

// Revision is one entry in a playlist's append-only content history (F17).
// ActorID is empty when the author has been anonymised (BR-4).
type Revision struct {
	ID         string
	PlaylistID string
	Rev        int
	ActorID    string
	Op         RevisionOp
	Detail     string
	CreatedAt  time.Time
}
