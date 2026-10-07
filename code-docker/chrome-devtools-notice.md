This browser is a separate container (code-docker-chrome), not part of code-docker. A person may be watching and using the same session over VNC.

- To open a dev server running in code-docker, forward its port first: `chrome-ports add 5173` (in code-docker), then open `http://localhost:5173`. Use `localhost`, not `127.0.0.1` or `code-docker`: Chrome resolves `localhost` to the forwarder, and nothing else reaches code-docker. For a server published inside dind: `chrome-ports add 8080 dind:8080`. Targets are limited to dev servers on code-docker and dind; code-docker's own web ports and dind's Docker API are refused.
- `chrome-ports` lists the forwards, and `chrome-ports rm <port>` removes one. Every page Chrome opens can reach a forwarded port, so remove the ones you're done with.
- Logins in this browser belong to its owner. Don't sign out, change account settings, or clear its data.
