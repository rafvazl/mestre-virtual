# 0006 — CLI com `flag` da stdlib e sem SDKs

- Status: aceito (restrição de ambiente; revisar quando houver acesso ao proxy)
- Data: 2026-10-18

## Contexto

O ambiente em que o projeto foi iniciado não tem acesso a
`proxy.golang.org`. Portanto não é possível adicionar dependências: nem
Cobra/urfave-cli para a CLI, nem SDKs de LLM (Anthropic, OpenAI, Ollama),
nem bibliotecas de log ou de teste. Esta não é uma preferência técnica, é
uma restrição.

## Decisão

- CLI só com `flag` da stdlib: um `FlagSet` por subcomando, despacho manual
  em `run(args, out)`; cada subcomando é uma função isolada (testável sem
  `os.Exit`).
- Nenhum SDK de LLM: `remote.StubProvider` falha explicitamente com
  `llm.ErrNotConfigured`; `local.EchoProvider` é um eco determinístico.
- Logging com `log/slog`, testes com `testing` (incluindo fuzz nativo).

## Alternativas consideradas

- **Vendorizar dependências manualmente**: possível, mas sem verificação
  de checksum pelo `sumdb` e com custo de manutenção alto.
- **Cliente HTTP próprio para a API remota**: viável só com `net/http`,
  mas reimplementaria (mal) retries, streaming SSE e tipos do SDK; adiado.

## Consequências

- Positivas: build reproduzível sem rede, `go.mod` sem dependências,
  superfície de supply chain zero.
- Negativas: ajuda e validação de flags mais pobres (flags só depois do
  subcomando, sem autocompletar, sem subcomandos aninhados declarativos);
  o modo `remote` não funciona de verdade.

## Plano de migração

1. **Cobra**: cada `cmdX(args, out)` vira o `RunE` de um `cobra.Command`;
   flags globais viram `PersistentFlags` no comando raiz. Manter `run()`
   como fachada para os testes existentes durante a transição.
2. **SDK remoto**: implementar `remote.AnthropicProvider` (ou outro) que
   satisfaça `llm.Provider`, com `Stream` mapeando eventos SSE em `Chunk` e
   observando `ctx`; trocar a factory; manter o stub para testes offline.
3. **Modelo local**: adapter Ollama/llama.cpp em `internal/llm/local`.
4. Ao adicionar dependências, fixar versões em `go.mod`, commitar `go.sum`
   e incluir `govulncheck` no CI.
