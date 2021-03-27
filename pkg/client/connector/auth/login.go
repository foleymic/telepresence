package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/pkg/browser"
	"golang.org/x/oauth2"

	"github.com/datawire/dlib/dcontext"
	"github.com/datawire/dlib/dgroup"
	"github.com/datawire/dlib/dhttp"
	"github.com/datawire/dlib/dlog"
	"github.com/telepresenceio/telepresence/rpc/v2/connector"
	"github.com/telepresenceio/telepresence/v2/pkg/client"
	"github.com/telepresenceio/telepresence/v2/pkg/client/connector/auth/authdata"
	"github.com/telepresenceio/telepresence/v2/pkg/client/connector/internal/scout"
)

const (
	callbackPath = "/callback"
)

type oauth2Callback struct {
	Code             string
	Error            string
	ErrorDescription string
}

type loginExecutor struct {
	// static
	env              client.Env
	SaveTokenFunc    func(context.Context, *oauth2.Token) error
	SaveUserInfoFunc func(context.Context, *authdata.UserInfo) error
	OpenURLFunc      func(string) error

	// stateful

	stdout    io.Writer
	scout     chan<- scout.ScoutReport
	callbacks chan oauth2Callback

	loginMu   sync.Mutex
	loginReq  chan context.Context
	loginResp chan error
}

// LoginExecutor controls the execution of a login flow
type LoginExecutor interface {
	Worker(ctx context.Context) error
	Login(ctx context.Context) error
}

// NewLoginExecutor returns an instance of LoginExecutor
func NewLoginExecutor(
	env client.Env,
	saveTokenFunc func(context.Context, *oauth2.Token) error,
	saveUserInfoFunc func(context.Context, *authdata.UserInfo) error,
	openURLFunc func(string) error,
	stdout io.Writer,
	scout chan<- scout.ScoutReport,
) LoginExecutor {
	return &loginExecutor{
		env:              env,
		SaveTokenFunc:    saveTokenFunc,
		SaveUserInfoFunc: saveUserInfoFunc,
		OpenURLFunc:      openURLFunc,

		stdout:    stdout,
		scout:     scout,
		callbacks: make(chan oauth2Callback),
		loginReq:  make(chan context.Context),
		loginResp: make(chan error),
	}
}

// EnsureLoggedIn will check if the user is logged in and if not initiate the login flow.
func EnsureLoggedIn(ctx context.Context, stdout io.Writer, scout chan<- scout.ScoutReport) (connector.LoginResult_Code, error) {
	if token, _ := authdata.LoadTokenFromUserCache(ctx); token != nil {
		return connector.LoginResult_OLD_LOGIN_REUSED, nil
	}

	if err := Login(ctx, stdout, scout); err != nil {
		return connector.LoginResult_UNSPECIFIED, err
	}

	return connector.LoginResult_NEW_LOGIN_SUCCEEDED, nil
}

func Login(ctx context.Context, stdout io.Writer, scout chan<- scout.ScoutReport) error {
	env, err := client.LoadEnv(ctx)
	if err != nil {
		return err
	}

	l := NewLoginExecutor(
		env,
		authdata.SaveTokenToUserCache,
		authdata.SaveUserInfoToUserCache,
		browser.OpenURL,
		stdout,
		scout,
	)
	grp := dgroup.NewGroup(ctx, dgroup.GroupConfig{
		ShutdownOnNonError: true,
	})
	grp.Go("worker", l.Worker)
	grp.Go("fncall", l.Login)
	return grp.Wait()
}

func (l *loginExecutor) Worker(ctx context.Context) error {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return err
	}
	oauth2Config := oauth2.Config{
		ClientID:    l.env.LoginClientID,
		RedirectURL: fmt.Sprintf("http://localhost:%d%s", listener.Addr().(*net.TCPAddr).Port, callbackPath),
		Endpoint: oauth2.Endpoint{
			AuthURL:  l.env.LoginAuthURL,
			TokenURL: l.env.LoginTokenURL,
		},
		Scopes: []string{"openid", "profile", "email"},
	}

	grp := dgroup.NewGroup(ctx, dgroup.GroupConfig{
		EnableWithSoftness: ctx == dcontext.HardContext(ctx),
		ShutdownOnNonError: true,
	})

	grp.Go("server-http", func(ctx context.Context) error {
		defer close(l.callbacks)

		sc := dhttp.ServerConfig{
			Handler: http.HandlerFunc(l.httpHandler),
		}
		return sc.Serve(ctx, listener)
	})
	grp.Go("actor", func(ctx context.Context) error {
		loginCtx := context.Background()
		var pkceVerifier CodeVerifier
		for {
			select {
			case loginCtx = <-l.loginReq:
				var err error
				pkceVerifier, err = NewCodeVerifier()
				if err != nil {
					return err
				}
				if err := l.startLogin(ctx, oauth2Config, pkceVerifier); err != nil {
					loginCtx = context.Background()
					maybeSend(l.loginResp, err)
				}
			case callback := <-l.callbacks:
				loginCtx = context.Background()
				token, err := l.handleCallback(ctx, callback, oauth2Config, pkceVerifier)
				if err != nil {
					l.scout <- scout.ScoutReport{
						Action: "login_failure",
						Metadata: map[string]interface{}{
							"error": err.Error(),
						},
					}
				} else {
					fmt.Fprintln(l.stdout, "Login successful.")
					_ = l.retrieveUserInfo(ctx, token)
					l.scout <- scout.ScoutReport{
						Action: "login_success",
					}
				}
				maybeSend(l.loginResp, err)
			case <-loginCtx.Done():
				maybeSend(l.loginResp, loginCtx.Err())
				loginCtx = context.Background()
				fmt.Fprintln(l.stdout, "Login aborted.")
				l.scout <- scout.ScoutReport{
					Action: "login_interrupted",
				}
			case <-ctx.Done():
				return nil
			}
		}
	})

	return grp.Wait()
}

func (l *loginExecutor) startLogin(ctx context.Context, oauth2Config oauth2.Config, pkceVerifier CodeVerifier) error {
	// create OAuth2 authentication code flow URL
	state := uuid.New().String()
	url := oauth2Config.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("code_challenge", pkceVerifier.CodeChallengeS256()),
		oauth2.SetAuthURLParam("code_challenge_method", PKCEChallengeMethodS256),
	)

	fmt.Fprintln(l.stdout, "Launching browser authentication flow...")
	if err := l.OpenURLFunc(url); err != nil {
		fmt.Fprintf(l.stdout, "Could not open browser, please access this URL: %v\n", url)
	}

	return nil
}

func (l *loginExecutor) Login(ctx context.Context) error {
	l.loginMu.Lock()
	defer l.loginMu.Unlock()
	l.loginReq <- ctx
	return <-l.loginResp
}

func maybeSend(ch chan<- error, err error) {
	select {
	case ch <- err:
	default:
	}
}

func (l *loginExecutor) handleCallback(
	ctx context.Context,
	callback oauth2Callback, oauth2Config oauth2.Config, pkceVerifier CodeVerifier,
) (*oauth2.Token, error) {
	if callback.Error != "" {
		return nil, fmt.Errorf("%v error returned on OAuth2 callback: %v", callback.Error, callback.ErrorDescription)
	}

	// retrieve access token from callback code
	token, err := oauth2Config.Exchange(
		ctx,
		callback.Code,
		oauth2.SetAuthURLParam("code_verifier", pkceVerifier.String()),
	)
	if err != nil {
		return nil, fmt.Errorf("error while exchanging code for token: %w", err)
	}

	err = l.SaveTokenFunc(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("could not save access token to user cache: %w", err)
	}

	return token, nil
}

func (l *loginExecutor) retrieveUserInfo(ctx context.Context, token *oauth2.Token) error {
	var userInfo authdata.UserInfo
	req, err := http.NewRequest("GET", l.env.UserInfoURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %v from user info endpoint", resp.StatusCode)
	}
	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	err = json.Unmarshal(content, &userInfo)
	if err != nil {
		return err
	}
	return l.SaveUserInfoFunc(ctx, &userInfo)
}

func (l *loginExecutor) httpHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != callbackPath {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	code := query.Get("code")
	errorName := query.Get("error")
	errorDescription := query.Get("error_description")

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><title>Authentication Successful</title></head><body>")
	if errorName == "" && code != "" {
		w.Header().Set("Location", l.env.LoginCompletionURL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		sb.WriteString("<h1>Authentication Successful</h1>")
		sb.WriteString("<p>You can now close this tab and resume on the CLI.</p>")
	} else {
		sb.WriteString("<h1>Authentication Error</h1>")
		sb.WriteString(fmt.Sprintf("<p>%s: %s</p>", errorName, errorDescription))
		w.WriteHeader(http.StatusInternalServerError)
	}
	sb.WriteString("</body></html>")

	w.Header().Set("Content-Type", "text/html")
	if _, err := io.WriteString(w, sb.String()); err != nil {
		dlog.Errorf(r.Context(), "Error writing callback response body: %v", err)
	}

	l.callbacks <- oauth2Callback{
		Code:             code,
		Error:            errorName,
		ErrorDescription: errorDescription,
	}
}
