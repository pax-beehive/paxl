package adaptor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/pax-oss/paxl/internal/model"
)

// NewDSHAdapter reads durable logs without starting DSH or loading credentials.
func NewDSHAdapter() Adapter {
	return &staticAdapter{
		agent: &model.AgentInfo{
			Name:       model.AgentNameDSH,
			Kind:       model.AgentKindLocal,
			Capability: model.AgentCapabilityLocalCLI,
			Command:    []string{"dsh", "--profile", "acp"},
			Reason:     "Local DSH session reading; prompt delivery and native resume are not supported.",
		},
		cliProbe:     func() bool { return commandExists("dsh") },
		sessionProbe: func() bool { root, err := dshSessionsRoot(); return err == nil && pathExists(root) },
		listSessions: listDSHSessions,
		getSession:   getDSHSession,
	}
}

func dshSessionsRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("PAXL_DSH_SESSIONS_DIR")); root != "" {
		return filepath.Abs(root)
	}
	home := strings.TrimSpace(os.Getenv("DSH_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve DSH home: %w", err)
		}
		home = filepath.Join(userHome, ".dsh")
	}
	return filepath.Abs(filepath.Join(home, "sessions"))
}

type dshHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	CWD       string `json:"cwd"`
	Origin    string `json:"origin"`
	Parent    string `json:"parentSession"`
}

type dshEvent struct {
	Raw     json.RawMessage `json:"-"`
	Type    string          `json:"type"`
	Seq     int64           `json:"seq"`
	Time    int64           `json:"time"`
	Surface json.RawMessage `json:"surfaceOp"`
	Data    json.RawMessage `json:"data"`
}

type dshReadResult struct {
	header    dshHeader
	session   *model.Session
	elements  []*model.Element
	model     string
	updatedMS int64
}

var dshFilename = regexp.MustCompile(`^session(?:\.v([1-9][0-9]*))?\.jsonl(?:\.zstd)?$`)

var errDSHOtherSession = errors.New("different DSH session")

func dshLogPaths(ctx context.Context) ([]string, error) {
	root, err := dshSessionsRoot()
	if err != nil {
		return nil, err
	}
	type generation struct {
		version int
		path    string
	}
	selected := map[string]generation{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		match := dshFilename.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil
		}
		version := 0
		if match[1] != "" {
			var parseErr error
			version, parseErr = strconv.Atoi(match[1])
			if parseErr != nil {
				return fmt.Errorf("invalid DSH generation")
			}
		}
		dir := filepath.Dir(path)
		previous, ok := selected[dir]
		if !ok || version > previous.version {
			selected[dir] = generation{version, path}
		}
		if ok && version == previous.version {
			return fmt.Errorf("ambiguous DSH generation in %s", dir)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var paths []string
	for _, item := range selected {
		paths = append(paths, item.path)
	}
	sort.Strings(paths)
	return paths, err
}

func listDSHSessions(ctx context.Context, req *ListSessionsRequest) (*ListSessionsResponse, error) {
	paths, err := dshLogPaths(ctx)
	if err != nil {
		return nil, err
	}
	sessions := map[string]*model.Session{}
	for _, path := range paths {
		result, err := readDSHLog(ctx, path, false, "")
		if err != nil {
			return nil, fmt.Errorf("read DSH log %s: %w", path, err)
		}
		if result.header.Origin == "subagent" && (req == nil || !req.IncludeSubagents) {
			continue
		}
		sessions[result.session.ID] = result.session
	}
	return sortedSessions(sessions, req), nil
}

func getDSHSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error) {
	if req == nil || strings.TrimSpace(req.NativeID) == "" {
		return nil, fmt.Errorf("native session id is required")
	}
	paths, err := dshLogPaths(ctx)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		result, err := readDSHLog(ctx, path, true, req.NativeID)
		if errors.Is(err, errDSHOtherSession) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read DSH log %s: %w", path, err)
		}
		if result != nil {
			return &GetSessionResponse{Elements: result.elements}, nil
		}
	}
	return nil, fmt.Errorf("DSH session %q not found", req.NativeID)
}

func readDSHLog(
	ctx context.Context,
	path string,
	history bool,
	nativeID string,
) (*dshReadResult, error) {
	file, err := os.Open(
		path,
	) // #nosec G304 -- Path comes from the configured log root scan, not the requested native ID.
	if err != nil {
		return nil, err
	}
	defer closeFile(file)
	var input io.Reader = file
	if strings.HasSuffix(path, ".zstd") {
		decoder, decodeErr := zstd.NewReader(
			file,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(64<<20),
		)
		if decodeErr != nil {
			return nil, fmt.Errorf("open DSH decompressor: %w", decodeErr)
		}
		defer decoder.Close()
		input = decoder
	}
	limited := &io.LimitedReader{R: input, N: 256 << 20}
	reader := bufio.NewReaderSize(limited, scannerMaxTokenSize)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return nil, fmt.Errorf("read DSH header: %w", err)
	}
	result := &dshReadResult{}
	if err := json.Unmarshal(line, &result.header); err != nil {
		return nil, fmt.Errorf("invalid DSH header")
	}
	h := &result.header
	if nativeID != "" && h.ID != nativeID {
		return nil, errDSHOtherSession
	}
	if h.Type != "session" || h.Version < 0 || h.Version > 2 || h.ID == "" || h.CreatedAt < 0 {
		return nil, fmt.Errorf("unsupported DSH header or format version %d", h.Version)
	}
	match := dshFilename.FindStringSubmatch(filepath.Base(path))
	if match == nil || firstNonEmpty(match[1], "0") != strconv.Itoa(h.Version) {
		return nil, fmt.Errorf("DSH filename and header version differ")
	}
	result.session = &model.Session{
		ID:        "dsh:" + h.ID,
		NativeID:  h.ID,
		Agent:     model.AgentNameDSH,
		Status:    "available",
		UpdatedAt: dshTime(h.CreatedAt),
	}
	result.updatedMS = h.CreatedAt
	if h.CWD != "" {
		result.session.ProjectID = h.CWD
		roots, _ := json.Marshal([]string{h.CWD})
		result.session.WorkspaceRootsJSON = string(roots)
	}
	if err := result.readEvents(ctx, reader, limited, history); err != nil {
		return nil, err
	}
	if result.session.Title == "" {
		result.session.Title = firstNonEmpty(
			titleCandidate(result.session.Preview),
			sessionProjectTitle(h.CWD),
			"DSH "+h.ID,
		)
	}
	result.session.LastActive = result.session.UpdatedAt
	return result, nil
}

func (r *dshReadResult) readEvents(
	ctx context.Context,
	reader *bufio.Reader,
	limited *io.LimitedReader,
	history bool,
) error {
	lastSeq := int64(-1)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadSlice('\n')
		if limited.N <= 0 {
			return fmt.Errorf("DSH log exceeds decoded size limit")
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		} // The live writer may not have completed its final line.
		if err != nil {
			return fmt.Errorf("read DSH event: %w", err)
		}
		var event dshEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("invalid DSH event")
		}
		event.Raw = append([]byte(nil), line...)
		if err := normalizeDSHPackedEvent(&event, r.header.Version); err != nil {
			return err
		}
		if event.Type == "" || event.Seq <= lastSeq || event.Time < 0 {
			return fmt.Errorf("invalid DSH event sequence")
		}
		lastSeq = event.Seq
		if err := r.observe(&event, history); err != nil {
			return err
		}
	}
}

func dshTime(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }

func (r *dshReadResult) observe(event *dshEvent, history bool) error {
	if event.Time > r.updatedMS {
		r.updatedMS = event.Time
		r.session.UpdatedAt = dshTime(event.Time)
	}
	if event.Type == "session/title" {
		var data struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(event.Data, &data) != nil {
			return fmt.Errorf("invalid DSH title event")
		}
		if strings.TrimSpace(data.Title) != "" {
			r.session.Title = strings.TrimSpace(data.Title)
		}
	}
	if event.Type == "request/context" {
		var data struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(event.Data, &data) == nil {
			r.model = data.Model
		}
	}
	return r.observeMessage(event, history)
}
