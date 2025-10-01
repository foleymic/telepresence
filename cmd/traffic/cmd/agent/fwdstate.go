package agent

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/datawire/dlib/dlog"
	"github.com/telepresenceio/telepresence/rpc/v2/manager"
	"github.com/telepresenceio/telepresence/v2/pkg/agentconfig"
	"github.com/telepresenceio/telepresence/v2/pkg/forwarder"
	"github.com/telepresenceio/telepresence/v2/pkg/iputil"
	"github.com/telepresenceio/telepresence/v2/pkg/restapi"
	"github.com/telepresenceio/telepresence/v2/pkg/tunnel"
)

type fwdState struct {
	*state
	intercept         agentconfig.InterceptTarget
	container         string
	forwarder         forwarder.Interceptor
	chosenInterceptId string
}

// generateMechanismDescription creates a human-readable description for the intercept mechanism.
func generateMechanismDescription(spec *manager.InterceptSpec) string {
	if spec.Mechanism != "http" {
		return "all TCP connections"
	}

	// Build HTTP filter description
	var filters []string

	// Add header filters
	for key, value := range spec.HeaderFilters {
		filters = append(filters, fmt.Sprintf("header %s=%s", key, value))
	}

	// Add path filters
	for _, path := range spec.PathFilters {
		filters = append(filters, fmt.Sprintf("path %s", path))
	}

	if len(filters) > 0 {
		return fmt.Sprintf("HTTP filters: %s", strings.Join(filters, ", "))
	}

	return "all HTTP connections"
}

// NewInterceptState creates an InterceptState that performs intercepts by using an Interceptor which indiscriminately
// intercepts all traffic to the port that it forwards.
func (s *state) NewInterceptState(forwarder forwarder.Interceptor, intercept agentconfig.InterceptTarget, container string) InterceptState {
	return &fwdState{
		state:     s,
		intercept: intercept,
		container: container,
		forwarder: forwarder,
	}
}

func (fs *fwdState) Target() agentconfig.InterceptTarget {
	return fs.intercept
}

func (fs *fwdState) InterceptInfo(ctx context.Context, callerID, path string, containerPort uint16, headers http.Header) (*restapi.InterceptInfo, error) {
	// The OSS agent is either intercepting or it isn't. There's no way to tell what it is that's being intercepted.
	fw := fs.forwarder
	if containerPort == 0 {
		return fw.InterceptInfo(), nil
	}
	port := fw.Target().Port()
	if containerPort == port {
		return fw.InterceptInfo(), nil
	}
	dlog.Debugf(ctx, "no match found for path %q, port %d, %s", path, containerPort, headers)
	return &restapi.InterceptInfo{Intercepted: false}, nil
}

type ProviderMux struct {
	AgentProvider   tunnel.ClientStreamProvider
	ManagerProvider tunnel.StreamProvider
}

func (pm *ProviderMux) ReportMetrics(ctx context.Context, metrics *manager.TunnelMetrics) {
	pm.AgentProvider.ReportMetrics(ctx, metrics)
}

func (pm *ProviderMux) CreateClientStream(ctx context.Context, tag tunnel.Tag, sessionID tunnel.SessionID, id tunnel.ConnID, roundTripLatency, dialTimeout time.Duration,
) (tunnel.Stream, error) {
	return pm.AgentProvider.CreateClientStream(ctx, tag, sessionID, id, roundTripLatency, dialTimeout)
}

func (fs *fwdState) HandlePort(ctx context.Context, cepts []*manager.InterceptInfo) []*manager.ReviewInterceptRequest {
	dlog.Debugf(ctx, "fwdState.HandlePort called with %d intercepts", len(cepts))

	var active []*manager.InterceptInfo
	var waiting []*manager.InterceptInfo
	for _, is := range cepts {
		switch is.Disposition {
		case manager.InterceptDispositionType_ACTIVE:
			active = append(active, is)
		case manager.InterceptDispositionType_WAITING:
			waiting = append(waiting, is)
		}
	}

	// Convert HTTP intercepts with headers to wiretaps BEFORE any other processing
	// This must be done early to prevent wiretap removal issues
	for _, ii := range waiting {
		if !ii.Spec.Wiretap && ii.Spec.Mechanism == "http" {
			// Check if this is an HTTP intercept with header patterns
			hasHeaderPatterns := false
			for _, arg := range ii.Spec.MechanismArgs {
				if strings.HasPrefix(arg, "--pattern=") {
					hasHeaderPatterns = true
					break
				}
			}
			if hasHeaderPatterns {
				// Treat HTTP header-based intercepts as wiretaps to allow multiple intercepts
				ii.Spec.Wiretap = true
				dlog.Debugf(ctx, "converted HTTP intercept %s to wiretap", ii.Id)
			}
		}
	}

	var activeIntercept *manager.InterceptInfo
	if fs.chosenInterceptId != "" {
		for _, is := range active {
			if fs.chosenInterceptId == is.Id {
				if !is.Spec.Wiretap {
					activeIntercept = is
				}
				break
			}
		}
	}

	if activeIntercept == nil {
		fs.chosenInterceptId = ""

		// Attach to already ACTIVE intercept if there is one.
		for _, is := range active {
			if !is.Spec.Wiretap {
				fs.chosenInterceptId = is.Id
				activeIntercept = is
				break
			}
		}
	}

	fwd := fs.forwarder
	if fs.sessionInfo != nil {
		// Update forwarding.
		fwd.SetStreamProvider(fs)
	}
	fwd.SetIntercepting(ctx, activeIntercept)

	// Remove inactive wiretaps.
	// But don't remove wiretaps that are in the waiting list and being converted to wiretaps
	waitingMap := make(map[string]bool)
	for _, ii := range waiting {
		waitingMap[ii.Id] = true
	}
	dlog.Debugf(ctx, "waitingMap contains: %v", waitingMap)

	// Debug: Check what's in the active list
	var activeWiretapIDs []string
	for _, ii := range active {
		if ii.Spec.Wiretap {
			activeWiretapIDs = append(activeWiretapIDs, ii.Id)
		}
	}
	dlog.Debugf(ctx, "active wiretap IDs: %v", activeWiretapIDs)

	for _, id := range fwd.WiretapIDs() {
		// Check if this wiretap should be kept
		shouldKeep := false

		// Keep wiretaps that are in the active list
		if slices.ContainsFunc(active, func(ii *manager.InterceptInfo) bool { return ii.Id == id && ii.Spec.Wiretap }) {
			shouldKeep = true
			dlog.Debugf(ctx, "keeping wiretap id %s (in active list)", id)
		}

		// Keep wiretaps that are in the waiting list (they might be converted to wiretaps)
		if !shouldKeep && waitingMap[id] {
			shouldKeep = true
			dlog.Debugf(ctx, "keeping wiretap id %s (in waiting list)", id)
		}

		// Keep wiretaps that correspond to HTTP intercepts with headers (they should be wiretaps)
		if !shouldKeep {
			// Check if this wiretap corresponds to an HTTP intercept with headers
			for _, ii := range active {
				if ii.Id == id && ii.Spec.Mechanism == "http" && len(ii.Spec.MechanismArgs) > 0 {
					// Check if this is an HTTP intercept with header patterns
					hasHeaderPatterns := false
					for _, arg := range ii.Spec.MechanismArgs {
						if strings.HasPrefix(arg, "--pattern=") {
							hasHeaderPatterns = true
							break
						}
					}
					if hasHeaderPatterns {
						shouldKeep = true
						dlog.Debugf(ctx, "keeping wiretap id %s (HTTP intercept with headers)", id)
						break
					}
				}
			}
		}

		// Check waiting list too for HTTP intercepts with headers
		if !shouldKeep {
			for _, ii := range waiting {
				if ii.Id == id && ii.Spec.Mechanism == "http" && len(ii.Spec.MechanismArgs) > 0 {
					// Check if this is an HTTP intercept with header patterns
					hasHeaderPatterns := false
					for _, arg := range ii.Spec.MechanismArgs {
						if strings.HasPrefix(arg, "--pattern=") {
							hasHeaderPatterns = true
							break
						}
					}
					if hasHeaderPatterns {
						shouldKeep = true
						dlog.Debugf(ctx, "keeping wiretap id %s (HTTP intercept with headers in waiting)", id)
						break
					}
				}
			}
		}

		if !shouldKeep {
			dlog.Debugf(ctx, "removing wiretap id %s", id)
			fwd.RemoveWiretap(id)
		}
	}

	// Add active wiretaps.
	for _, ii := range active {
		if ii.Spec.Wiretap {
			if !fwd.HasWiretap(ii.Id) {
				dlog.Debugf(ctx, "adding wiretap id %s to %s", ii.Id, iputil.JoinHostPort(ii.Spec.TargetHost, uint16(ii.Spec.TargetPort)))
				fwd.AddWiretap(ii)
			} else {
				dlog.Debugf(ctx, "wiretap id %s already exists in forwarder", ii.Id)
			}
		}
	}

	// Also add wiretaps from waiting list that have been converted
	for _, ii := range waiting {
		if ii.Spec.Wiretap {
			if !fwd.HasWiretap(ii.Id) {
				dlog.Debugf(ctx, "adding converted wiretap id %s to %s", ii.Id, iputil.JoinHostPort(ii.Spec.TargetHost, uint16(ii.Spec.TargetPort)))
				fwd.AddWiretap(ii)
			} else {
				dlog.Debugf(ctx, "converted wiretap id %s already exists in forwarder", ii.Id)
			}
		}
	}

	// Review waiting intercepts
	reviews := make([]*manager.ReviewInterceptRequest, 0, len(waiting))
	for _, ii := range waiting {

		switch {
		case activeIntercept == nil || ii.Spec.Wiretap:
			// This intercept is ready to be active
			container := ii.Spec.ContainerName
			if container == "" {
				container = fs.container
			}
			cs := fs.containerStates[container]
			if cs == nil {
				reviews = append(reviews, &manager.ReviewInterceptRequest{
					Id:                ii.Id,
					Disposition:       manager.InterceptDispositionType_AGENT_ERROR,
					Message:           fmt.Sprintf("No match for container %q", container),
					MechanismArgsDesc: generateMechanismDescription(ii.Spec),
				})
				continue
			}
			if !ii.Spec.Wiretap {
				// For HTTP intercepts with headers, allow multiple intercepts
				// For other intercepts, we can only have one active intercept that isn't a wiretap
				if ii.Spec.Mechanism == "http" && len(ii.Spec.MechanismArgs) > 0 {
					// Check if this is an HTTP intercept with header patterns
					hasHeaderPatterns := false
					for _, arg := range ii.Spec.MechanismArgs {
						if strings.HasPrefix(arg, "--pattern=") {
							hasHeaderPatterns = true
							break
						}
					}
					if hasHeaderPatterns {
						// Allow multiple HTTP intercepts with headers
						// Don't set activeIntercept to allow multiple
					} else {
						// Regular HTTP intercept without headers - only allow one
						activeIntercept = ii
					}
				} else {
					// Non-HTTP intercept - only allow one
					activeIntercept = ii
				}
			}
			// Process mechanism args to set up header patterns for http mechanism
			headerPatterns := make(map[string]string) // "headerName=headerValue" -> "true" (just for existence check)
			mechanismDesc := "all TCP connections"

			if ii.Spec.Mechanism == "http" {
				mechanismDesc = "HTTP requests"
				// Parse mechanism args for header pattern configuration
				for _, arg := range ii.Spec.MechanismArgs {
					if strings.HasPrefix(arg, "--pattern=") {
						patternPart := strings.TrimPrefix(arg, "--pattern=")
						// Split into header name and value
						parts := strings.SplitN(patternPart, "=", 2)
						if len(parts) == 2 {
							headerPatterns[parts[0]] = parts[1] // Store the actual pattern value
						}
					}
				}
				if len(headerPatterns) > 0 {
					var patternList []string
					for name, value := range headerPatterns {
						patternList = append(patternList, fmt.Sprintf("%s=%s", name, value))
					}
					mechanismDesc = fmt.Sprintf("HTTP requests with header patterns: %s", strings.Join(patternList, ", "))
				}

				// Store header patterns in the intercept info for the HTTP interceptor to use
				ii.Headers = headerPatterns
			}

			review := &manager.ReviewInterceptRequest{
				Id:                ii.Id,
				Disposition:       manager.InterceptDispositionType_ACTIVE,
				PodIp:             fs.PodIP().String(),
				FtpPort:           int32(fs.FtpPort()),
				SftpPort:          int32(fs.SftpPort()),
				MountPoint:        cs.MountPoint(),
				Mounts:            cs.Mounts().ToRPC(),
				MechanismArgsDesc: mechanismDesc,
				Environment:       cs.Env(),
			}

			// Set header patterns if we have any
			if len(headerPatterns) > 0 {
				review.Headers = headerPatterns
			}

			reviews = append(reviews, review)
		default:
			// We already have an intercept in play, so reject this one.
			chosenID := activeIntercept.Id
			dlog.Infof(ctx, "Setting intercept %q as AGENT_ERROR; as it conflicts with %q as the current chosen-to-be-ACTIVE intercept", ii.Id, chosenID)
			var msg string
			if activeIntercept.Disposition == manager.InterceptDispositionType_ACTIVE {
				msg = fmt.Sprintf("Conflicts with the currently-served intercept %q", chosenID)
			} else {
				msg = fmt.Sprintf("Conflicts with the currently-waiting-to-be-served intercept %q", chosenID)
			}
			reviews = append(reviews, &manager.ReviewInterceptRequest{
				Id:                ii.Id,
				Disposition:       manager.InterceptDispositionType_AGENT_ERROR,
				Message:           msg,
				MechanismArgsDesc: generateMechanismDescription(ii.Spec),
			})
		}
	}
	return reviews
}
