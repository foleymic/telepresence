## Run the HTTP intercepts integration suite

This guide assumes:
- You already have kubectl configured for your target cluster (KUBECONFIG set or default).
- You are running commands from the repository root.

### Prerequisites
- Go (version compatible with this repo)
- kubectl
- helm
- Docker (recommended if using a local cluster like Docker Desktop)

### One-time setup (build binaries/images)
At minimum, the tests need a `telepresence` binary in `build-output/bin`. The simplest way is:

```bash
export TELEPRESENCE_VERSION=v2.25.1 or your working version
make build
```

If you’re using a local cluster (Docker Desktop, Rancher Desktop), also build images locally to avoid pushing to a registry:

```bash
export TELEPRESENCE_VERSION=v2.22.0-alpha.0
export TELEPRESENCE_REGISTRY=local
make build client-image tel2-image
```

If you use a remote registry (e.g., GitHub Container Registry), set:

```bash
export TELEPRESENCE_VERSION=v2.22.0-alpha.0
export TELEPRESENCE_REGISTRY=ghcr.io/telepresenceio
```

Optional (override kubeconfig if needed):

```bash
export DEV_KUBECONFIG="$KUBECONFIG"
```

### Run only the HTTPIntercepts suite
The suite name is `HTTPIntercepts`. Use the `TEST_SUITE` filter and run the integration tests module:

```bash
TEST_SUITE='^HTTPIntercepts$' go test ./integration_test -v -count=1
```

This will:
- Create ephemeral namespaces `telepresence-<suffix>` and `ambassador-<suffix>`
- Deploy an `echo` service in the app namespace
- Connect Telepresence to the manager automatically
- Execute the HTTP intercept tests (headers, paths, coexistence, conflicts, scale)
- Clean up after completion

### Reuse an existing traffic-manager (e.g., ambassador)
If you already have a traffic-manager installed and want tests to use it (and not uninstall it), set:
NOTE - if using namespaces scoping in the traffic manager deployment, you will have to also set GITHUB_SHA and specify the namespace
in the values.yml - e.g.
```
namespaces:
- default
- telepresence-tpdev01
```

```bash
export GITHUB_SHA=tpdev01 #static suffix for the test namespace.  Needed if using namespace scoping in traffic manager (see note above)
export TELEPRESENCE_TEST_MANAGER_NAMESPACE=ambassador
TEST_SUITE='^HTTPIntercepts$' go test ./integration_test -v -count=1
```

Behavior when set:
- Tests will connect to the existing manager in `TELEPRESENCE_TEST_MANAGER_NAMESPACE`.
- The manager namespace will not be created or modified by the harness.
- Teardown will not delete the manager namespace or uninstall the manager.
- The harness will not impersonate the test ServiceAccount; your current kube user is used.

### Preserve cluster state (skip teardown)
Set this to keep namespaces, workloads, and the connection after tests:

```bash
export TELEPRESENCE_TEST_SKIP_TEARDOWN=1
```

When set:
- No uninstall of the manager, no namespace deletions, no workload deletions
- The Telepresence connection is not quit at the end

### Run an individual test within the suite
To run a single test method (example: `Test_HTTPHeaderFiltering`):

```bash
go test ./integration_test -v -testify.m='^Test_HTTPHeaderFiltering$' -count=1
```

Tip: Combine with `TEST_SUITE` if you want to be explicit:

```bash
TEST_SUITE='^HTTPIntercepts$' go test ./integration_test -v -testify.m='^Test_HTTPHeaderFiltering$' -count=1
```

### Notes and troubleshooting
- If a traffic-manager is already installed in your cluster in a non-ephemeral namespace, uninstall it before running the tests. The harness manages its own install in `ambassador-<suffix>`.
- Using Docker Desktop + `TELEPRESENCE_REGISTRY=local` avoids pushing images and uses the local Docker cache.
- Log locations: see `CONTRIBUTING.md` for detailed logging paths and tips.

### Reference
- Test file: `integration_test/http_intercepts_test.go`
- Suite name: `HTTPIntercepts`
- Harness env: see `CONTRIBUTING.md` “Environment Variables for Integration Testing”


