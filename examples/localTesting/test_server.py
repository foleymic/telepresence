#!/usr/bin/env python3
"""
Robust Python HTTP server for testing Telepresence intercepts.
This server will respond to requests on port 23001 with the /info endpoint.
"""

import json
import sys
import signal
import threading
import time
from http.server import HTTPServer, BaseHTTPRequestHandler
from datetime import datetime

class TestHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        """Handle GET requests"""
        try:
            if self.path == '/info':
                self.send_info_response()
            elif self.path == '/health':
                self.send_health_response()
            else:
                self.send_404_response()
        except Exception as e:
            print(f"[{datetime.now()}] Error handling request: {e}")
            self.send_500_response()

    @property
    def server_port(self):
        """Get the server port from the server instance"""
        return self.server.server_address[1]

    def send_info_response(self):
        """Send info response"""
        info_data = {
            "service": "com-manh-cp-composer",
            "local_port": self.server_port,
            "version": "local-dev",
            "environment": "development",
            "timestamp": datetime.now().isoformat(),
            "status": "running",
            "message": "This is the local development server",
            "headers_received": dict(self.headers)
        }

        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()

        response = json.dumps(info_data, indent=2)
        self.wfile.write(response.encode())

        print(f"[{datetime.now()}] GET /info - 200 OK")
        print(f"  Headers: {dict(self.headers)}")

    def send_health_response(self):
        """Send health check response"""
        health_data = {
            "status": "UP",
            "timestamp": datetime.now().isoformat(),
            "service": "com-manh-cp-composer-local",
            "uptime": time.time() - server_start_time
        }

        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()

        response = json.dumps(health_data, indent=2)
        self.wfile.write(response.encode())

        print(f"[{datetime.now()}] GET /health - 200 OK")

    def send_404_response(self):
        """Send 404 response"""
        self.send_response(404)
        self.send_header('Content-type', 'application/json')
        self.end_headers()

        error_data = {
            "error": "Not Found",
            "message": f"Path {self.path} not found",
            "timestamp": datetime.now().isoformat(),
            "available_endpoints": ["/info", "/health"]
        }

        response = json.dumps(error_data, indent=2)
        self.wfile.write(response.encode())

        print(f"[{datetime.now()}] GET {self.path} - 404 Not Found")

    def send_500_response(self):
        """Send 500 response"""
        self.send_response(500)
        self.send_header('Content-type', 'application/json')
        self.end_headers()

        error_data = {
            "error": "Internal Server Error",
            "timestamp": datetime.now().isoformat()
        }

        response = json.dumps(error_data, indent=2)
        self.wfile.write(response.encode())

        print(f"[{datetime.now()}] GET {self.path} - 500 Internal Server Error")

    def log_message(self, format, *args):
        """Override to reduce log noise"""
        pass

# Global variable to track server start time
server_start_time = time.time()

def signal_handler(sig, frame):
    """Handle Ctrl+C gracefully"""
    print(f"\n[{datetime.now()}] Received interrupt signal, shutting down server...")
    sys.exit(0)

def main():
    import argparse

    parser = argparse.ArgumentParser(description='Test server for Telepresence intercepts')
    parser.add_argument('--port', '-p', type=int, default=23001,
                       help='Port to run the server on (default: 23001)')
    args = parser.parse_args()

    port = args.port
    server_address = ('', port)

    # Set up signal handler
    signal.signal(signal.SIGINT, signal_handler)

    print(f"Starting robust test server on port {port}")
    print(f"Server start time: {datetime.now().isoformat()}")
    print(f"Available endpoints:")
    print(f"  - GET /info - Returns service information")
    print(f"  - GET /health - Returns health status")
    print(f"  - Any other path returns 404")
    print(f"\nTo test with Telepresence intercept:")
    print(f"  curl -H 'x-intercept-id: user1' http://localhost:{port}/info")
    print(f"  curl -H 'x-intercept-id: user1' http://localhost:{port}/health")
    print(f"\nTo test without intercept (should go to remote service):")
    print(f"  curl http://localhost:{port}/info")
    print(f"  curl http://localhost:{port}/health")
    print(f"\nPress Ctrl+C to stop the server")
    print("-" * 50)

    try:
        httpd = HTTPServer(server_address, TestHandler)
        print(f"[{datetime.now()}] Server started successfully on port {port}")
        httpd.serve_forever()
    except KeyboardInterrupt:
        print(f"\n[{datetime.now()}] Server stopped by user")
        sys.exit(0)
    except OSError as e:
        if e.errno == 48:  # Address already in use
            print(f"Error: Port {port} is already in use")
            print("Please stop the existing server or use a different port")
        else:
            print(f"Error starting server: {e}")
        sys.exit(1)
    except Exception as e:
        print(f"Unexpected error: {e}")
        sys.exit(1)

if __name__ == '__main__':
    main()
