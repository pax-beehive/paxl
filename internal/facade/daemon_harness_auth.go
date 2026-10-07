package facade

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

type DaemonHarnessAuthClient interface {
	HarnessAuthLogin(
		context.Context,
		string,
		*model.DaemonHarnessAuthLoginCommand,
	) (*model.DaemonCommandAck, error)
	HarnessAuthStatus(context.Context, string, string) (*model.DaemonQueryResult, error)
}

type DaemonHarnessAuthFacade struct{ client DaemonHarnessAuthClient }

func NewDaemonHarnessAuthFacade(client DaemonHarnessAuthClient) *DaemonHarnessAuthFacade {
	if client == nil {
		client = NewDaemonUnixClient("")
	}
	return &DaemonHarnessAuthFacade{client: client}
}

type DaemonHarnessAuthStatusRequest struct {
	Harness   string
	SessionID string
}

func (f *DaemonHarnessAuthFacade) Status(
	ctx context.Context,
	req *DaemonHarnessAuthStatusRequest,
	opts ...func(*Option),
) (*model.DaemonHarnessAuthView, error) {
	if req == nil {
		return nil, fmt.Errorf("authentication status request is required")
	}
	harness, err := model.ParseDaemonAuthHarness(req.Harness)
	if err != nil {
		return nil, fmt.Errorf("check daemon authentication: %w", err)
	}
	if len(req.SessionID) > 128 || strings.TrimSpace(req.SessionID) != req.SessionID {
		return nil, fmt.Errorf("invalid login session id")
	}
	verbosef(applyOptions(opts), "Checking harness authentication through paxd.")
	result, err := f.client.HarnessAuthStatus(ctx, string(harness), req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("check daemon authentication: %w", daemonAPIGuidance(err))
	}
	if result == nil {
		return nil, fmt.Errorf("daemon returned no authentication status")
	}
	if result.Error != nil {
		return nil, fmt.Errorf("check daemon authentication: %w", result.Error)
	}
	if err := validateHarnessAuthView(result.HarnessAuth, string(harness)); err != nil {
		return nil, err
	}
	return result.HarnessAuth, nil
}

func validateHarnessAuthView(view *model.DaemonHarnessAuthView, harness string) error {
	if view == nil || view.Harness != harness {
		return fmt.Errorf(
			"daemon returned no valid authentication status for the requested harness; update paxd to a version with harness authentication support",
		)
	}
	if _, err := model.ParseDaemonHarnessAuthState(string(view.State)); err != nil {
		return fmt.Errorf("decode daemon authentication status: %w", err)
	}
	return nil
}

func (c *DaemonLocalAPIClient) HarnessAuthStatus(
	ctx context.Context,
	harness string,
	sessionID string,
) (*model.DaemonQueryResult, error) {
	path := "/v1/harnesses/" + url.PathEscape(harness) + "/auth/status"
	if sessionID != "" {
		path += "?session_id=" + url.QueryEscape(sessionID)
	}
	return c.get(ctx, path)
}

type DaemonHarnessAuthLoginRequest struct {
	Harness   string
	Operation model.DaemonHarnessAuthOperation
	Code      string
	Method    string
	Wait      bool
}

func (c *DaemonLocalAPIClient) HarnessAuthLogin(
	ctx context.Context,
	commandID string,
	cmd *model.DaemonHarnessAuthLoginCommand,
) (*model.DaemonCommandAck, error) {
	return c.postCommand(
		ctx,
		"/v1/harnesses/"+url.PathEscape(cmd.Harness)+"/auth/login",
		cmd,
		commandID,
	)
}

func (f *DaemonHarnessAuthFacade) Login(
	ctx context.Context,
	req *DaemonHarnessAuthLoginRequest,
	opts ...func(*Option),
) (*model.DaemonHarnessAuthView, error) {
	if err := validateHarnessLoginRequest(req); err != nil {
		return nil, err
	}
	// Limit only this client's wait. The daemon owns the independent five-minute
	// process lifetime, so a canceled client never cancels the login itself.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	verbosef(applyOptions(opts), "Requesting harness login through paxd.")
	harness, _ := model.ParseDaemonAuthHarness(req.Harness)
	sessionID := ""
	if req.Operation == model.DaemonHarnessAuthSubmit ||
		req.Operation == model.DaemonHarnessAuthCancel {
		current, err := f.Status(
			ctx,
			&DaemonHarnessAuthStatusRequest{Harness: string(harness)},
			opts...)
		if err != nil {
			return nil, err
		}
		if current.SessionID == "" {
			return nil, fmt.Errorf("no pending login for this harness")
		}
		if req.Operation == model.DaemonHarnessAuthSubmit &&
			current.State != model.DaemonHarnessAuthAwaitingCode {
			return nil, fmt.Errorf("login is not waiting for an authorization code")
		}
		sessionID = current.SessionID
	}
	ack, err := f.client.HarnessAuthLogin(
		ctx,
		newDaemonCommandID(),
		&model.DaemonHarnessAuthLoginCommand{
			Harness:   string(harness),
			Operation: req.Operation,
			Code:      req.Code,
			Method:    req.Method,
			SessionID: sessionID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("request daemon login: %w", daemonAPIGuidance(err))
	}
	if ack == nil {
		return nil, fmt.Errorf("daemon returned no login acknowledgement")
	}
	if ack.Error != nil {
		return nil, fmt.Errorf("request daemon login: %w", ack.Error)
	}
	if !ack.OK || ack.Result == nil {
		return nil, fmt.Errorf("daemon did not accept login")
	}
	view := ack.Result.HarnessAuth
	if err := validateHarnessAuthView(view, string(harness)); err != nil {
		return nil, err
	}
	if view.SessionID == "" {
		return nil, fmt.Errorf("daemon returned no login handle")
	}
	if sessionID != "" && view.SessionID != sessionID {
		return nil, fmt.Errorf("daemon returned a different login handle")
	}
	if req.Operation == model.DaemonHarnessAuthCancel {
		return view, nil
	}
	if req.Wait {
		return f.waitForLogin(ctx, req, view, opts...)
	}
	return view, harnessLoginOutcome(view)
}

func validateHarnessLoginRequest(req *DaemonHarnessAuthLoginRequest) error {
	if req == nil {
		return fmt.Errorf("login request is required")
	}
	if _, err := model.ParseDaemonAuthHarness(req.Harness); err != nil {
		return fmt.Errorf("request daemon login: %w", err)
	}
	harness, _ := model.ParseDaemonAuthHarness(req.Harness)
	switch req.Operation {
	case model.DaemonHarnessAuthOperationUnknown:
		return fmt.Errorf("unsupported login operation")
	case model.DaemonHarnessAuthStart:
		if err := model.ValidateDaemonAuthMethod(
			harness,
			req.Method,
			req.Code != "",
			false,
		); err != nil {
			return err
		}
	case model.DaemonHarnessAuthSubmit:
		if err := model.ValidateDaemonAuthMethod(harness, req.Method, false, true); err != nil {
			return err
		}
		if req.Code == "" {
			return fmt.Errorf("authorization code is required")
		}
	case model.DaemonHarnessAuthCancel:
		if req.Code != "" || req.Method != "" {
			return fmt.Errorf("cancel accepts no code or method")
		}
	default:
		return fmt.Errorf("unsupported login operation")
	}
	if len(req.Code) > 4096 || strings.ContainsAny(req.Code, "\r\n\x00") ||
		strings.TrimSpace(req.Code) != req.Code {
		return fmt.Errorf("secret input must be one non-empty line of at most 4096 bytes")
	}
	return nil
}

func (f *DaemonHarnessAuthFacade) waitForLogin(
	ctx context.Context,
	req *DaemonHarnessAuthLoginRequest,
	view *model.DaemonHarnessAuthView,
	opts ...func(*Option),
) (*model.DaemonHarnessAuthView, error) {
	sessionID := view.SessionID
	for harnessLoginPending(view) {
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return view, fmt.Errorf(
				"stopped waiting for login; paxd keeps the attempt until its deadline, check with paxl auth status --harness %s: %w",
				req.Harness,
				ctx.Err(),
			)
		case <-timer.C:
		}
		next, err := f.Status(
			ctx,
			&DaemonHarnessAuthStatusRequest{Harness: req.Harness, SessionID: sessionID},
			opts...)
		if err != nil {
			return view, err
		}
		if next.SessionID != sessionID {
			return view, fmt.Errorf("daemon returned a different login handle")
		}
		view = next
	}
	return view, harnessLoginOutcome(view)
}

func harnessLoginPending(view *model.DaemonHarnessAuthView) bool {
	return view.State == model.DaemonHarnessAuthStarting ||
		view.State == model.DaemonHarnessAuthExchanging
}

func harnessLoginOutcome(view *model.DaemonHarnessAuthView) error {
	if view.State == model.DaemonHarnessAuthFailed ||
		view.State == model.DaemonHarnessAuthExpired ||
		view.State == model.DaemonHarnessAuthCancelled {
		return fmt.Errorf(
			"harness login %s; run paxl auth login --harness %s to retry",
			view.State, view.Harness,
		)
	}
	return nil
}
