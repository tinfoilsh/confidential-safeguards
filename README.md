# Confidential Safeguards

Confidential Safeguards checks conversations on Tinfoil's model router and chat harness against an Acceptable Use Policy, without anyone at Tinfoil ever being able to read them. It runs as a sidecar inside the caller's enclave and is reachable only from containers in that enclave.

## What is sent where

- **Conversations go only to attested models.** Each conversation goes to a classifier model (`gpt-oss-safeguard-120b`) in its own attested enclave. If it flags a violation, an independent reviewer model (`kimi-k3`, also attested) decides, and its verdict is final.
- **The control plane learns only that a confirmed violation occurred and whose it is.** The report holds the user's own credential and a conversation id, never the messages, the rule broken, or the models' reasoning.
- **Nothing is logged.** No content, categories, or model reasoning.
- **The setup is auditable.** The image, policy text, and settings are declared in the enclave configuration, so attestation covers them.

## Development

Run the tests, which need no network access:

```sh
go test -race ./...
```

Run locally with `TINFOIL_API_KEY` and `SAFEGUARD_POLICY` set (see [.env.example](.env.example)):

```sh
go run .
```

## Reporting Vulnerabilities

Please report security vulnerabilities by either:

- Emailing [security@tinfoil.sh](mailto:security@tinfoil.sh)
- Opening an issue on GitHub on this repository

We aim to respond to (legitimate) security reports within 24 hours.
