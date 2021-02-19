//go:generate ./generic.gen ConnectRequest *github.com/datawire/telepresence2/rpc/v2/connector.ConnectRequest

package setonce

type Behavior interface {
	isPanic() bool
}

type behavior bool

func (b behavior) isPanic() bool { return bool(b) }

var (
	SecondSetIsIgnored Behavior = behavior(false)
	SecondSetIsPanic   Behavior = behavior(true)
)
