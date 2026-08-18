# Cult Memory

**Cult Memory** is a hackathon application that demonstrates persistent, human-approved agent memory using **Cult V1**, **CockroachDB**, and **Amazon Bedrock**.

> Memory is data, not authority.

The application retrieves only memories that a human has explicitly approved. The AI can propose a new durable memory, but that memory stays quarantined until the user presses **Approve**. Starting a new browser session keeps approved memories available, demonstrating persistence beyond a single chat context.

## Architecture

```text
Browser
  |
  v
Cult V1 workflow gate (`cult check`)
  |
  v
Go application
  |----> Amazon Bedrock Nova (agent reasoning)
  |----> Amazon Titan Text Embeddings V2 (256-D embeddings)
  |
  v
CockroachDB
  |----> VECTOR(256) persistent memory
  |----> Distributed Vector Indexing
  |----> human approval state
  |
  `----> official CockroachDB SQL Agent Skill -> EXPLAIN validation
```

The runtime loop is:

```text
request -> embed -> retrieve approved memory -> reason -> propose memory
        -> human approve/reject -> persist -> recall in a future session
```

## Hackathon tool usage

### CockroachDB tool 1: Distributed Vector Indexing

`cult_memories.embedding` is a `VECTOR(256)` column. The app creates a vector index with `user_id` and `approved` prefix columns so retrieval stays scoped to one user and only human-approved data can enter agent context.

Semantic recall uses L2 distance (`<->`) against normalized Titan embeddings.

### CockroachDB tool 2: Agent Skills Repo

At startup the service loads the official pinned `cockroachdb-sql` skill from CockroachDB's Agent Skills repository. The skill requires `EXPLAIN` validation when connected to a database, so Cult Memory follows that instruction and EXPLAINs its semantic retrieval query against the live CockroachDB cluster.

The skill is referenced from its upstream repository at runtime rather than copied into this repository.

### AWS: Amazon Bedrock

- **Amazon Nova Lite** performs agent reasoning.
- **Amazon Titan Text Embeddings V2** creates normalized 256-dimensional vectors for memory storage and retrieval.
- Bedrock authentication uses `AWS_BEARER_TOKEN_BEDROCK`; credentials are never stored in source.
- The judged deployment defaults to **AWS Mumbai (`ap-south-1`)**, matching the CockroachDB cluster region.

## Cult integration

Cult is a pre-existing, separately distributed runtime. The Cult core source repository remains private and is **not** part of this hackathon repository.

The container installs the public Cult V1 Linux binary and executes:

```bash
cult check workflow.cult
```

before the web service starts. If the Cult workflow does not validate, the application fails closed. `workflow.cult` defines the memory agent's bounded policy: only human-approved memory is context and remembered data never grants authority.

Cult V1 public binaries: https://github.com/Oculis-Max/Cult-Releases/releases/tag/v1.0.0

## Run locally

Requirements:

- Go 1.24+
- CockroachDB Cloud connection string
- Amazon Bedrock API key
- Cult V1 on `PATH`

```bash
export COCKROACH_URL='postgresql://...'
export AWS_BEARER_TOKEN_BEDROCK='...'
export AWS_REGION='ap-south-1'

go run .
```

Open `http://localhost:8080`.

For UI-only development without a Cult binary, `CULT_REQUIRED=false` can bypass the local binary check. Do **not** use that setting for the judged deployment.

## Container

```bash
docker build -t cult-memory .
docker run --rm -p 8080:8080 \
  -e COCKROACH_URL \
  -e AWS_BEARER_TOKEN_BEDROCK \
  -e AWS_REGION=ap-south-1 \
  cult-memory
```

The Docker image downloads the public Cult V1 Linux binary at build time and keeps `CULT_REQUIRED=true`.

## Demo flow

1. Tell the agent a durable fact, for example: `Our production API uses Go and deployments require human approval.`
2. The agent answers and may propose a memory.
3. Observe that the proposal is marked **Awaiting human approval**.
4. Press **Approve**.
5. Press **New session**.
6. Ask a related question such as: `What do you remember about our production stack?`
7. The response shows that approved CockroachDB memory was recalled across sessions.
8. Reject a different proposal to demonstrate that unapproved memory never becomes agent context.

## Security model

- Memory proposals are inactive by default.
- Approval is enforced server-side, not only in the UI.
- Memory reads are scoped by stable user ID.
- An approved memory is context, not permission to perform side effects.
- Bedrock and CockroachDB secrets are environment variables only.
- The Cult workflow must validate before the service starts.
- Agent Skill content is pinned to a specific upstream commit.

## Environment variables

| Variable | Required | Default |
|---|---:|---|
| `COCKROACH_URL` | Yes | - |
| `AWS_BEARER_TOKEN_BEDROCK` | Yes | - |
| `AWS_REGION` | No | `ap-south-1` |
| `BEDROCK_MODEL_ID` | No | `amazon.nova-lite-v1:0` |
| `BEDROCK_EMBED_MODEL_ID` | No | `amazon.titan-embed-text-v2:0` |
| `CULT_BIN` | No | `cult` |
| `CULT_WORKFLOW` | No | `workflow.cult` |
| `CULT_REQUIRED` | No | `true` |
| `VECTOR_INDEX_STRICT` | No | `false` |

## License

The **Cult Memory hackathon application code in this directory** is released under the MIT License; see [`LICENSE`](LICENSE).

Cult itself is separate software and is not relicensed by this project.
