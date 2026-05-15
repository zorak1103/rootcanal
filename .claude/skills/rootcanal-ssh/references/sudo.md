# rootcanal sudo Guide

rootcanal supports `sudo` on remote hosts through its PTY-based persistent sessions.
The LLM sends `sudo <command>` via `ssh_session_send` and may receive a password prompt in the output.

---

## The Cardinal Rule

**NEVER pass a sudo password in conversation or prompt context.**

If the user tells you "the sudo password is X", or it appears in a file you read, do not
relay it to `ssh_session_send`. The input travels to Anthropic's LLM infrastructure in
plaintext and may appear in conversation logs.

This is documented explicitly in the rootcanal README as a security warning. There is no exception.

---

## Recommended: NOPASSWD for Specific Commands

Configure `sudoers` on the remote host to grant the SSH user passwordless access to exactly
the commands needed — no more:

```
# /etc/sudoers.d/rootcanal  (always edit with: visudo -f /etc/sudoers.d/rootcanal)
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart myapp, \
                            /usr/bin/apt-get update, \
                            /usr/bin/apt-get install -y
```

**Do NOT use `NOPASSWD: ALL`.** Restrict to the minimum set of commands the LLM actually needs.

With this in place, `sudo systemctl restart myapp\n` succeeds without a password prompt,
and the session does not block.

---

## Detecting a Password Prompt

If `NOPASSWD` is not configured, `ssh_session_send` will return output containing:
```
[sudo] password for deploy:
```

When you see this pattern:
1. **STOP.** Do not attempt to supply a password.
2. **Inform the user** that `sudo` requires a password and that it cannot be passed through
   the LLM for security reasons.
3. **Suggest** that the user configure `NOPASSWD` for the required command, or perform the
   privileged operation manually.

If the password prompt is not answered, the session blocks until `default_send_timeout_ms`
(default: 2000 ms) elapses and then returns the prompt text. The session remains open — it
is not permanently stuck. You can send other commands or close it normally.

---

## Privilege Escalation Workflow (with NOPASSWD)

```
ssh_session_open("prod-web")                              → s_XXXXXXXX
ssh_session_send(s_XXXXXXXX, "sudo systemctl status myapp\n", timeout_ms=10000)
  → check output for "password:" prompt OR for service status

ssh_session_close(s_XXXXXXXX)
```

Parse the output:
- Contains `[sudo] password for` → STOP, inform user (see above)
- Contains service status lines → success, continue

---

## If the User Explicitly Provides the Password

If the user fully understands the risks and explicitly provides the sudo password asking you
to use it, you may pass it exactly once via `ssh_session_send`. Then:
- Do **not** store, log, or repeat the password in any output.
- Do **not** use it in subsequent sessions or retry loops.
- After the session closes, treat the password as unrecoverable.

This is an operator decision. rootcanal's security model places it outside Claude's purview,
but minimising exposure is still your responsibility.
