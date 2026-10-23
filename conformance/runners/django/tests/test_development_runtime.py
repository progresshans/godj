"""Real HTTP framing controls for the independent development observer."""
import json
import socketserver
import threading
import unittest

from conformance.runners.django.development_reference.runtime import Client


class DevelopmentHTTPTests(unittest.TestCase):
    def request(self, body, declared_length):
        response = (b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n"
                    b"Connection: close\r\nContent-Length: " + str(declared_length).encode() + b"\r\n\r\n" + body)
        errors = []

        class Handler(socketserver.BaseRequestHandler):
            def handle(self):
                try:
                    self.request.settimeout(2)
                    request = b""
                    while not request.endswith(b"\r\n\r\n"):
                        part = self.request.recv(4096)
                        if not part or len(request) + len(part) > 4096:
                            raise RuntimeError("HTTP probe request is incomplete or oversized")
                        request += part
                    self.request.sendall(response)
                except Exception as error:
                    errors.append(error)

        with socketserver.TCPServer(("127.0.0.1", 0), Handler) as server:
            server.timeout = 3
            thread = threading.Thread(target=server.handle_request)
            thread.start()
            try:
                value = Client("127.0.0.1:" + str(server.server_address[1])).request("GET", "/state/")
            finally:
                thread.join(timeout=4)
                self.assertFalse(thread.is_alive(), "HTTP probe did not stop")
            self.assertEqual(errors, [])
            return value

    def test_incomplete_body_cannot_be_a_successful_observation(self):
        complete = b'{"version":"v2","pid":7}'
        for partial in (b"", complete[:6]):
            with self.subTest(bytes_received=len(partial)):
                interrupted = self.request(partial, len(complete))
                self.assertEqual((interrupted.status, interrupted.body), (0, b""))
                # A later complete response still supplies the actual value.
                recovered = self.request(complete, len(complete))
                self.assertEqual(recovered.status, 200)
                self.assertEqual(recovered.json(), {"version": "v2", "pid": 7})

    def test_complete_invalid_json_remains_a_failure(self):
        for body in (b"", b"not-json"):
            with self.subTest(body=body):
                response = self.request(body, len(body))
                self.assertEqual(response.status, 200)
                with self.assertRaises(json.JSONDecodeError):
                    response.json()


if __name__ == "__main__":
    unittest.main()
