# A2A Examples

---

## Recipe 1: Expose an LLMAgent over A2A behind auth middleware

**Goal:** Wrap an `LLMAgent` as an A2A server; require bearer-token auth on
every request; declare the security scheme on the agent card.

```go
package main

import (
    "log"
    "net/http"
    "os"

    oasis "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/a2a"
    "github.com/nevindra/oasis/provider/gemini"
)

func main() {
    llm := gemini.New(os.Getenv("GEMINI_API_KEY"), "gemini-2.0-flash")

    agent := oasis.NewLLMAgent(
        "research-assistant",
        "Answers research questions with web-level knowledge",
        llm,
        oasis.WithPrompt("You are a research assistant. Answer questions concisely and cite sources."),
    )

    srv := a2a.NewServer(agent,
        a2a.WithURL("https://agents.example.com"),
        a2a.WithVersion("1.0.0"),
        a2a.WithSecurityScheme("bearer", a2a.SecurityScheme{
            HTTPAuth: &a2a.HTTPAuthSecurityScheme{
                Scheme:       "Bearer",
                BearerFormat: "JWT",
            },
        }),
        a2a.WithSkill(a2a.AgentSkill{
            ID:          "research",
            Name:        "Research",
            Description: "Answer questions and summarize findings",
            Tags:        []string{"research", "qa"},
        }),
    )
    defer srv.Close()

    // Auth is middleware — Oasis does not build an auth framework.
    // The card declares the scheme; your middleware enforces it.
    handler := requireBearerToken(os.Getenv("API_SECRET"), srv)

    log.Println("listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", handler))
}

// requireBearerToken is a minimal example middleware. In production use a
// proper JWT validator, not a string comparison.
func requireBearerToken(secret string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // The agent card endpoint must be publicly readable for discovery.
        if r.URL.Path == a2a.WellKnownCardPath && r.Method == http.MethodGet {
            next.ServeHTTP(w, r)
            return
        }
        if r.Header.Get("Authorization") != "Bearer "+secret {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**Plain-English walkthrough:**
- `a2a.NewServer` wraps any `core.Agent`; the agent never needs to know it is
  being served over A2A.
- `WithSecurityScheme` populates `securitySchemes` on the agent card — it is a
  declaration, not enforcement.
- Auth enforcement belongs in the `http.Handler` layer that wraps `*Server`.
  The card endpoint (`/.well-known/agent-card.json`) is usually public so
  clients can discover the required scheme before authenticating.
- `defer srv.Close()` cancels background task runs if the process shuts down.

---

## Recipe 2: Consume a remote A2A agent as a Network child

**Goal:** Dial a remote A2A agent and use it as a peer inside a Network so the
LLM router can delegate to it alongside local agents.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    oasis "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/a2a"
    "github.com/nevindra/oasis/network"
    "github.com/nevindra/oasis/provider/gemini"
)

func main() {
    ctx := context.Background()

    llm := gemini.New(os.Getenv("GEMINI_API_KEY"), "gemini-2.0-flash")

    // Dial the remote agent. It satisfies core.Agent — no adapter needed.
    // WithBearerToken attaches the credential to every request (card fetch included).
    remoteAnalyst, err := a2a.Dial(ctx,
        "https://analytics.partner.example.com",
        a2a.WithBearerToken(os.Getenv("PARTNER_API_KEY")),
    )
    if err != nil {
        log.Fatal(err)
    }

    // A local agent in the same Network.
    localWriter := oasis.NewLLMAgent(
        "writer",
        "Writes clear, concise reports from data summaries",
        llm,
    )

    // remoteAnalyst.Name() returns the sanitized card name, e.g. "analytics_agent".
    // Network generates the tool "agent_analytics_agent" for the router LLM.
    net := network.New(
        "report-coordinator",
        "Coordinates data analysis and report writing",
        llm,
        network.WithChildren(remoteAnalyst, localWriter),
    )

    result, err := net.Execute(ctx, oasis.AgentTask{
        Input: "Analyze last quarter's sales data and write an executive summary",
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.Output)
}
```

**Plain-English walkthrough:**
- `a2a.Dial` returns `*RemoteAgent` which satisfies `core.Agent`. `network.WithChildren`
  accepts any `core.Agent`, so `remoteAnalyst` drops in without an adapter.
- The Network router sees `agent_analytics_agent` (sanitized from the card name)
  as a regular tool call. It has no idea the child is remote.
- Cross-process cancellation: when `ctx` is cancelled, the in-flight remote task
  is abandoned on the client side (the remote server may still be running it).
  Use `remoteAnalyst.Client().CancelTask` for explicit cancellation.

---

## Recipe 3: LLM-driven delegation via AsTool

**Goal:** Attach a remote A2A agent as a tool on a local `LLMAgent` so the LLM
can decide when to delegate — without a Network.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    oasis "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/a2a"
    "github.com/nevindra/oasis/provider/gemini"
)

func main() {
    ctx := context.Background()

    llm := gemini.New(os.Getenv("GEMINI_API_KEY"), "gemini-2.0-flash")

    // Dial the remote translation agent.
    remoteTranslator, err := a2a.Dial(ctx,
        "https://translate.partner.example.com",
        a2a.WithBearerToken(os.Getenv("PARTNER_API_KEY")),
    )
    if err != nil {
        log.Fatal(err)
    }

    // AsTool wraps the remote agent as a core.AnyTool with a single "input" parameter.
    // The tool name and description come from the remote agent's card.
    translationTool := a2a.AsTool(remoteTranslator)

    agent := oasis.NewLLMAgent(
        "assistant",
        "Multilingual assistant",
        llm,
        oasis.WithPrompt("You are a multilingual assistant. Use the translation tool when the user asks to translate text."),
        oasis.WithTools(translationTool),
    )

    result, err := agent.Execute(ctx, oasis.AgentTask{
        Input: "Translate 'Hello, how are you?' to Japanese and Spanish",
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.Output)
}
```

**Plain-English walkthrough:**
- `AsTool` converts a `*RemoteAgent` to a `core.AnyTool`. The tool takes one
  `"input"` string parameter; the LLM fills in the natural-language task.
- A remote task failure (network error, failed state) becomes a `ToolResult.Error`
  that the LLM sees and can react to — it never aborts the local agent run.
- Use `AsTool` when you want occasional delegation to one remote agent without the
  overhead of a Network routing loop. Use Network when you have multiple remote agents
  and want LLM-driven selection.

---

## Recipe 4: Long-running task with push notifications

**Goal:** Submit a long-running document job non-blocking, register a webhook to
receive the result asynchronously, and acknowledge it without polling.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"

    oasis "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/a2a"
    "github.com/nevindra/oasis/core"
    "github.com/nevindra/oasis/provider/gemini"
)

// --- Server side: expose the long-running agent -------------------------

func startServer() {
    llm := gemini.New(os.Getenv("GEMINI_API_KEY"), "gemini-2.0-flash")

    agent := oasis.NewLLMAgent(
        "document-processor",
        "Processes large documents and extracts structured data",
        llm,
    )

    srv := a2a.NewServer(agent,
        a2a.WithURL("https://jobs.example.com"),
        a2a.WithPushNotifications(), // must opt in
    )
    defer srv.Close()
    log.Fatal(http.ListenAndServe(":8080", srv))
}

// --- Client side: blocking Execute (Option A) ---------------------------

func submitJobBlocking(ctx context.Context) {
    remote, err := a2a.Dial(ctx, "https://jobs.example.com")
    if err != nil {
        log.Fatal(err)
    }

    // Blocking: Execute polls GetTask until the task terminates.
    result, err := remote.Execute(ctx, core.AgentTask{
        Input: "Extract all dates and amounts from the attached contract",
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("task completed:", result.Output)
}

// --- Client side: non-blocking push send (Option B) --------------------

func submitJobNonBlocking(ctx context.Context) {
    remote, err := a2a.Dial(ctx, "https://jobs.example.com")
    if err != nil {
        log.Fatal(err)
    }

    // Non-blocking: returns immediately with a working task; the server
    // POSTs the terminal StreamResponse to the registered webhook URL.
    msg := a2a.Message{
        MessageID: core.NewID(),
        Role:      a2a.RoleUser,
        Parts:     []a2a.Part{a2a.TextPart("Extract all dates and amounts from the attached contract")},
    }
    task, err := remote.Client().SendMessage(ctx, msg, &a2a.SendConfiguration{
        Blocking: false,
        PushNotificationConfig: &a2a.PushNotificationConfig{
            URL:   "https://my-service.example.com/webhook",
            Token: "my-secret-token",
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("submitted, task ID:", task.ID, "state:", task.Status.State) // state: TASK_STATE_WORKING
}

// --- Webhook receiver ---------------------------------------------------

func webhookHandler(w http.ResponseWriter, r *http.Request) {
    // Validate the notification token.
    if r.Header.Get("X-A2A-Notification-Token") != "my-secret-token" {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    var sr a2a.StreamResponse
    if err := json.NewDecoder(r.Body).Decode(&sr); err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }

    if sr.Task != nil {
        fmt.Println("task completed:", sr.Task.ID, "state:", sr.Task.Status.State)
        for _, art := range sr.Task.Artifacts {
            for _, p := range art.Parts {
                if p.Text != "" {
                    fmt.Println("result:", p.Text)
                }
            }
        }
    }

    w.WriteHeader(http.StatusOK)
}
```

**Plain-English walkthrough:**
- The server must opt in via `WithPushNotifications()`. Without it, the push
  config methods return `ErrPushNotSupported`.
- Option A (`RemoteAgent.Execute`) always uses the blocking path — it polls
  `GetTask` every 500 ms until the task settles.
- Option B uses `remote.Client().SendMessage` with `SendConfiguration{Blocking: false}`
  and a `PushNotificationConfig` carrying the webhook URL and token. The HTTP
  response returns immediately with `TASK_STATE_WORKING`; the server POSTs the
  terminal `StreamResponse` to the webhook once the task settles.
- `X-A2A-Notification-Token` echoes back the `Token` field from the config.
  Validate it in your webhook handler to authenticate the server.
- Push delivery is best-effort and fire-and-forget (logged on failure, no
  retries). The authoritative task state is always available via `GetTask`.

---

## Recipe 5: Multi-turn HITL (human-in-the-loop) across the wire

**Goal:** An agent suspends mid-task to ask the user a question; the client
surfaces the question, waits for input, and resumes the same task over the wire.

```go
package main

import (
    "bufio"
    "context"
    "fmt"
    "log"
    "net/http"
    "os"

    oasis "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/a2a"
    "github.com/nevindra/oasis/core"
    "github.com/nevindra/oasis/provider/gemini"
)

// --- Server side: an agent that suspends to ask a clarifying question ---

func main() {
    // Start the server in a goroutine for this example.
    go serveHITLAgent()

    ctx := context.Background()

    // Dial the agent. The same ThreadID is the key to resume — the client
    // automatically carries the pending task ID on the next Execute call.
    remote, err := a2a.Dial(ctx, "http://localhost:8081")
    if err != nil {
        log.Fatal(err)
    }

    threadID := "thread-" + core.NewID()
    scanner := bufio.NewScanner(os.Stdin)

    input := "Generate a financial report"
    for {
        result, err := remote.Execute(ctx, core.AgentTask{
            Input:    input,
            ThreadID: threadID,
        })
        if err != nil {
            log.Fatal(err)
        }

        switch result.FinishReason {
        case core.FinishStop:
            fmt.Println("Agent:", result.Output)
            return

        case core.FinishSuspended:
            // SuspendPayload carries the agent's question.
            question := string(result.SuspendPayload)
            if question == "" {
                question = result.Output
            }
            fmt.Println("Agent asks:", question)
            fmt.Print("Your answer: ")
            if !scanner.Scan() {
                return
            }
            // The next Execute on the same ThreadID resumes the remote task.
            input = scanner.Text()

        default:
            fmt.Println("unexpected finish reason:", result.FinishReason)
            return
        }
    }
}

func serveHITLAgent() {
    llm := gemini.New(os.Getenv("GEMINI_API_KEY"), "gemini-2.0-flash")

    // An agent that uses the typed HITL suspend protocol to ask a clarifying
    // question. WithInputHandler wires the ask_user tool which the LLM calls
    // when it needs human input; the framework translates that into FinishSuspended.
    // See docs/processors for processor-based HITL alternatives.
    agent := oasis.NewLLMAgent(
        "finance-assistant",
        "Generates financial reports — asks for fiscal year if not specified",
        llm,
        oasis.WithPrompt(
            "You generate financial reports. If the fiscal year is not specified, "+
                "call ask_user to ask which fiscal year they want before generating the report.",
        ),
    )

    srv := a2a.NewServer(agent, a2a.WithURL("http://localhost:8081"))
    defer srv.Close()
    log.Fatal(http.ListenAndServe(":8081", srv))
}
```

**Plain-English walkthrough:**

- The server translates `FinishSuspended` to `TASK_STATE_INPUT_REQUIRED` and
  records the task ID. The status message carries the agent's question.
- `RemoteAgent.Execute` returns `AgentResult{FinishReason: FinishSuspended}`.
  `SuspendPayload` holds the question text from the remote status message.
- The same `ThreadID` on the next `Execute` call causes `RemoteAgent` to
  inject the pending task ID into the outbound message automatically. The
  remote server resumes the suspended run instead of starting a new one.
- Re-suspension works: if the agent suspends again (e.g. to ask a second
  clarifying question), the loop repeats. The pending task ID is refreshed
  each time.
- Resume over `SendStreamingMessage` is not supported server-side. If you
  pass `core.WithStream(ch)` on a resume turn, `RemoteAgent` falls back to
  a blocking `SendMessage` automatically (the stream channel is still closed).
