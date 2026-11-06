package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/agendacraft/touchstone/internal/agent"
	"github.com/agendacraft/touchstone/internal/config"
)

type Turn struct {
	Request string          `json:"request_sha256"`
	Output  string          `json:"output"`
	Usage   agent.Usage     `json:"usage"`
	Cost    *agent.Cost     `json:"cost"`
	Error   agent.ErrorKind `json:"error,omitempty"`
}

type Recording struct {
	Version     int                     `json:"version"`
	Fingerprint string                  `json:"case_sha256"`
	CapturedAt  string                  `json:"captured_at"`
	Policy      map[string]config.Layer `json:"policy"`
	Turns       map[string][]Turn       `json:"turns"`
}

type session struct {
	mu        sync.Mutex
	recording Recording
	position  map[string]int
	live      agent.Agent
	replay    bool
	err       error
}

func newSession(c Case, source fs.FS, file string, live agent.Agent, replay bool) (*session, error) {
	policy := config.Defaults().ReviewLayers()
	policy["verification"] = config.Defaults().Verification()
	s := &session{
		live: live, replay: replay, position: map[string]int{},
		recording: Recording{Version: 1, Fingerprint: c.Fingerprint,
			CapturedAt: time.Now().UTC().Format(time.RFC3339), Policy: policy, Turns: map[string][]Turn{}},
	}
	if replay {
		data, err := fs.ReadFile(source, file)
		if err != nil {
			return nil, err
		}
		s.recording = Recording{}
		if err := decode(data, &s.recording); err != nil {
			return nil, errors.New("invalid recording")
		}
		if s.recording.Version != 1 || s.recording.Fingerprint != c.Fingerprint || len(s.recording.Policy) != 3 {
			return nil, errors.New("recording does not match case or policy version")
		}
		for _, name := range []string{"correctness", "security", "verification"} {
			if _, ok := s.recording.Policy[name]; !ok {
				return nil, errors.New("recording missing model policy")
			}
		}
		for name := range s.recording.Turns {
			if _, ok := policy[name]; !ok {
				return nil, errors.New("recording has unknown layer")
			}
		}
	}
	return s, nil
}

func (s *session) config() config.Config {
	layers := map[string]config.Layer{}
	for name, layer := range s.recording.Policy {
		layer.Model = "eval-" + name
		layers[name] = layer
	}
	return config.Defaults().WithLayers(layers)
}

func requestHash(r agent.Request) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.ReplaceAll(r.Prompt, r.WorkingDirectory, "<scratch>")))
	for _, name := range []string{"diff.patch", "candidates.json"} {
		data, _ := os.ReadFile(filepath.Join(r.WorkingDirectory, name))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *session) Run(ctx context.Context, r agent.Request) (agent.Result, error) {
	if err := ctx.Err(); err != nil {
		return agent.Result{}, err
	}
	name := strings.TrimPrefix(r.Model, "eval-")
	hash := requestHash(r)
	if s.replay {
		return s.play(name, hash)
	}
	r.Model = s.recording.Policy[name].Model
	result, err := s.live.Run(ctx, r)
	turn := Turn{Request: hash, Output: result.Output, Usage: result.Usage, Cost: result.Cost}
	if err != nil {
		turn.Error = agent.ProcessFailure
		var runErr *agent.RunError
		if errors.As(err, &runErr) {
			turn.Error = runErr.Kind
		}
	}
	s.mu.Lock()
	s.recording.Turns[name] = append(s.recording.Turns[name], turn)
	s.mu.Unlock()
	return result, err
}

func (s *session) play(name, hash string) (agent.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.recording.Turns[name]
	i := s.position[name]
	if i >= len(turns) || turns[i].Request != hash {
		s.err = errors.New("recording request drift or missing turn")
		return agent.Result{}, s.err
	}
	s.position[name]++
	turn := turns[i]
	result := agent.Result{Output: turn.Output, Usage: turn.Usage, Cost: turn.Cost}
	if turn.Error != "" {
		return result, &agent.RunError{Kind: turn.Error, ExitCode: -1}
	}
	return result, nil
}

func (s *session) finish() error {
	if s.err != nil {
		return s.err
	}
	if s.replay {
		for name, turns := range s.recording.Turns {
			if s.position[name] != len(turns) {
				return errors.New("recording has unused turns")
			}
		}
	}
	return nil
}

func writeJSON(file string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o600)
}
