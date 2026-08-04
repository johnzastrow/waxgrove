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
type Candidate struct {
	Title      string
	Artist     string
	Album      string
	DurationMS int
	ISRC       string
	MBID       string
	Year       int
	// Raw is the original text for free-text sources, preserved so an
	// unresolved item is never silently dropped (BR-5).
	Raw string
	// SourceRef names the adapter that produced this candidate.
	SourceRef string
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
