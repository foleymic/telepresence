```
NewState

.AddClient(*rpc.ClientInfo) (sessionID string)
.AddAgent(*rpc.AgentInfo) (sessionID string)

.Mark(sessionID string) (ok bool)
.Remove(sessionID string)
.SessionDone(sessionID string) <-chan struct{}
.WatchAgents(sessionID string) <-chan []*AgentInfo
.WatchIntercepts(sessionID string) <-chan []*InterceptInfo

.AddIntercept(sessionID string, spec InterceptSpec) (*InterceptInfo, error)
.RemoveIntercept(sessionID, interceptID string)
.ReviewIntercept(sessionID, interceptID string, state DispositionType, message string) error
```


`tel list` shouldn't required root-daemon
