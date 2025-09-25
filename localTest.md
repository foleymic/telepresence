# Multi-Pattern Header Routing - Local Testing Guide

This guide provides step-by-step instructions to test the multi-pattern header routing feature for Telepresence HTTP intercepts on a local computer running Docker.

## Prerequisites

- Docker Desktop installed and running
- Go 1.21+ installed
- kubectl installed
- Helm installed (`brew install helm` on macOS)

## Step 1: Install and Setup KIND

### Install KIND
```bash
# On macOS
brew install kind

# On Linux
curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64
chmod +x ./kind
sudo mv ./kind /usr/local/bin/kind
```

### Create KIND Cluster
```bash
# Create a KIND cluster named 'telepresence-test'
kind create cluster --name telepresence-test

# Verify cluster is running
kubectl cluster-info --context kind-telepresence-test
```

## Step 2: Build Telepresence

### Build the Binary and Docker Images
```bash
# Set environment variables
export TELEPRESENCE_VERSION=v2.22.0-alpha.1
export TELEPRESENCE_REGISTRY=local

# Build the binary and Docker images
make build client-image tel2-image
```

### Load Images into KIND
```bash
# Load the built images into the KIND cluster
kind load docker-image local/telepresence:2.22.0-alpha.0 --name telepresence-test
kind load docker-image local/tel2:2.22.0-alpha.0 --name telepresence-test
```

## Step 3: Setup Test Python Servers

### Create Server Directories and Files
```bash
# Create directories for the two servers
mkdir -p /tmp/server1 /tmp/server2

# Create index.html for Server 1 (Port 8080)
cat > /tmp/server1/index.html << 'EOF'
<!DOCTYPE html>
<html>
<head>
    <title>Server 1 - Port 8080</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; background-color: #e3f2fd; }
        .header { color: #1976d2; font-size: 24px; font-weight: bold; }
        .info { color: #424242; margin: 20px 0; }
    </style>
</head>
<body>
    <div class="header">🚀 Server 1 - Port 8080</div>
    <div class="info">This is the response from Python HTTP Server running on port 8080</div>
    <div class="info">Header: x-intercept-id=user1</div>
    <div class="info">Timestamp: <script>document.write(new Date().toISOString())</script></div>
</body>
</html>
EOF

# Create index.html for Server 2 (Port 8181)
cat > /tmp/server2/index.html << 'EOF'
<!DOCTYPE html>
<html>
<head>
    <title>Server 2 - Port 8181</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; background-color: #f3e5f5; }
        .header { color: #7b1fa2; font-size: 24px; font-weight: bold; }
        .info { color: #424242; margin: 20px 0; }
    </style>
</head>
<body>
    <div class="header">🎯 Server 2 - Port 8181</div>
    <div class="info">This is the response from Python HTTP Server running on port 8181</div>
    <div class="info">Header: x-intercept-id=user2</div>
    <div class="info">Timestamp: <script>document.write(new Date().toISOString())</script></div>
</body>
</html>
EOF
```

### Start the Python Servers
```bash
# Start Server 1 on port 8080
cd /tmp/server1 && python3 -m http.server 8080 &

# Start Server 2 on port 8181
cd /tmp/server2 && python3 -m http.server 8181 &

# Verify servers are running
lsof -i :8080 -i :8181
```

### Test the Servers
```bash
# Test Server 1 (should show blue-themed response)
curl -s localhost:8080/index.html

# Test Server 2 (should show purple-themed response)
curl -s localhost:8181/index.html
```

## Step 4: Install Traffic Manager

### Install Telepresence Traffic Manager
```bash
# Install the traffic manager using Helm
./build-output/bin/telepresence helm install --set logLevel=debug,image.pullPolicy=Never,agent.image.pullPolicy=Never

# Verify traffic manager is running
kubectl get pods -n ambassador
```

## Step 5: Deploy Test Application

### Create Hello World Deployment
```bash
# Create a simple nginx deployment
kubectl create deployment hello-world --image=nginx

# Expose the deployment as a service
kubectl expose deployment hello-world --port=80 --target-port=80

# Verify deployment is running
kubectl get pods -l app=hello-world
kubectl get svc hello-world
```

## Step 6: Create Multi-Pattern Intercept

### Create the Intercept with Multiple Header Patterns
```bash
# Create intercept with multiple header patterns
./build-output/bin/telepresence intercept hello-world --port 8080 --http-header="x-intercept-id=user1"

./build-output/bin/telepresence intercept hello-world --port 8181 --http-header="x-intercept-id=user2"

# Verify intercept is active
./build-output/bin/telepresence list
```

The output should show:
```
deployment hello-world: intercepted
   Intercept name    : hello-world
   State             : ACTIVE
   Workload kind     : Deployment
   Intercepting      : 10.244.0.77 -> 127.0.0.1
       80 -> 8080 TCP
   Intercepting      : HTTP requests with header patterns: map[x-intercept-id=user1:true]
   Volume Mount Point: /var/folders/fz/2kskhydd0xs566f9zbmfbf300000gq/T/telfs-2814790994
   Intercept name    : hello-world
   State             : ACTIVE
   Workload kind     : Deployment
   Intercepting      : 10.244.0.77 -> 127.0.0.1
       80 -> 8181 TCP
   Intercepting      : HTTP requests with header patterns: map[x-intercept-id=user2:true]
   Volume Mount Point: /var/folders/fz/2kskhydd0xs566f9zbmfbf300000gq/T/telfs-4106259446
```

## Step 7: Test Multi-Pattern Routing

### Test 1: Request with user1 Header (Routes to Port 8080)
```bash
curl -v -H "x-intercept-id:user1" http://hello-world.default.svc.cluster.local
```

**Expected Result:**
- Should return the blue-themed response from Server 1
- Shows "🚀 Server 1 - Port 8080"
- Background color: light blue (#e3f2fd)
- Header color: blue (#1976d2)

### Test 2: Request with user2 Header (Routes to Port 8181)
```bash
curl -v -H "x-intercept-id:user2" http://hello-world.default.svc.cluster.local
```

**Expected Result:**
- Should return the purple-themed response from Server 2
- Shows "🎯 Server 2 - Port 8181"
- Background color: light purple (#f3e5f5)
- Header color: purple (#7b1fa2)

### Test 3: Request without Header (Routes to Original Service)
```bash
curl -v http://hello-world.default.svc.cluster.local
```

**Expected Result:**
- Should return the nginx welcome page
- Shows "Welcome to nginx!"
- This confirms requests without matching headers go to the original service

## Step 8: Test Failure Scenarios

### Stop One Server and Test
```bash
# Stop Server 2 (port 8181)
pkill -f "python3 -m http.server 8181"

# Test user2 header (should fail)
curl -v -H "x-intercept-id:user2" http://hello-world.default.svc.cluster.local
```

**Expected Result:**
- Should return "Empty reply from server" or connection error
- This demonstrates that the routing correctly attempts to reach the specified port

### Test user1 Header (Should Still Work)
```bash
curl -v -H "x-intercept-id:user1" http://hello-world.default.svc.cluster.local
```

**Expected Result:**
- Should still return the blue-themed response from Server 1
- Confirms that other patterns continue to work when one fails

## Step 9: Cleanup

### Remove Intercept
```bash
./build-output/bin/telepresence leave hello-world
```

### Stop Python Servers
```bash
pkill -f "python3 -m http.server"
```

### Clean Up KIND Cluster
```bash
kind delete cluster --name telepresence-test
```

## Troubleshooting

### Common Issues

1. **"No agent found" error:**
   - Restart the hello-world deployment: `kubectl rollout restart deployment/hello-world`
   - Wait for the new pod to start and try creating the intercept again

2. **"Empty reply from server":**
   - Check if the Python servers are running: `lsof -i :8080 -i :8181`
   - Restart the servers if needed

3. **Traffic manager not starting:**
   - Check if Helm is installed: `helm version`
   - Verify KIND cluster is running: `kubectl cluster-info`

4. **Images not loading:**
   - Ensure Docker is running
   - Rebuild and reload images: `make build client-image tel2-image && kind load docker-image ...`

### Debug Commands

```bash
# Check intercept status
./build-output/bin/telepresence list

# Check agent logs
kubectl logs -l app=hello-world -c traffic-agent

# Check traffic manager logs
kubectl logs -n ambassador deployment/traffic-manager

# Check if servers are accessible
curl -s localhost:8080/index.html
curl -s localhost:8181/index.html
```

## Summary

This test validates the multi-pattern header routing feature by:

1. ✅ **Multiple header patterns** - Single intercept handles multiple header values
2. ✅ **Dynamic port routing** - Different headers route to different local ports
3. ✅ **Conditional routing** - Requests without headers go to original service
4. ✅ **Failure handling** - Graceful handling when local services are unavailable
5. ✅ **Visual distinction** - Different colored responses make it easy to verify routing

The feature enables multiple developers to work on the same service simultaneously, each with their own local development environment, while maintaining the ability to route requests to the original service when no specific header is provided.
