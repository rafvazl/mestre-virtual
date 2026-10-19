# 0002 — Abstração `llm.Provider` e factory fora do pacote

- Status: aceito
- Data: 2026-10-18

## Contexto

O jogo precisa trocar de backend de LLM (modelo local via Ollama/llama.cpp,
APIs remotas como Anthropic/OpenAI, um eco determinístico para testes) sem
mexer em regras, estado ou memória. Os adapters dependem do contrato
(`Request`, `Chunk`, erros sentinela), e quem monta a aplicação precisa
escolher um adapter a partir de configuração.

## Decisão

- `internal/llm` define só o contrato: `Provider` com `Name`, `Generate` e
  `Stream`, mais `Request/Response/Chunk` e erros sentinela
  (`ErrNotConfigured`, `ErrNoRoute`).
- Adapters ficam em subpacotes (`internal/llm/local`, `internal/llm/remote`)
  e importam `internal/llm`.
- A escolha por configuração fica em `internal/llm/factory`, **fora** de
  `internal/llm`: se a factory morasse no pacote do contrato, ele importaria
  os adapters, que importam o contrato — ciclo de import.
- Preocupações transversais entram como decorators que também implementam
  `Provider` (ex.: `obs.LoggingProvider`, `llm.RoutingProvider`).

## Alternativas consideradas

- **Factory dentro de `internal/llm` com registro via `init()`** (padrão
  `database/sql`): evita o pacote extra, mas exige imports em branco e
  esconde quais adapters estão compilados.
- **Interface só com `Generate`**: mais simples, mas a CLI quer narração
  em streaming; acrescentar `Stream` depois quebraria todos os adapters.
- **Usar um SDK diretamente no núcleo**: impossível no ambiente (sem proxy)
  e acoplaria o domínio a um fornecedor.

## Consequências

- Positivas: núcleo testável com fakes; trocar de modelo é mudar a factory;
  decorators se compõem.
- Negativas: um pacote a mais só para montagem. O contrato é o menor
  denominador comum: recursos específicos (tool use, cache de prompt, JSON
  mode) não aparecem na interface e, se forem necessários, exigem
  estendê-la ou fazer type assertion.
- `Stream` impõe um contrato que cada adapter precisa cumprir: fechar o
  canal ao terminar e observar `ctx` (senão a goroutine vaza). Isso é
  coberto por testes de cancelamento, mas não é verificável em tempo de
  compilação.
