# mestre-virtual

Mestre virtual de RPG de mesa em Go: uma CLI que narra a aventura, aplica regras e controla NPCs. A geração de texto vem de um LLM (local ou remoto); **estado, regras e memória ficam no núcleo em Go** — o LLM nunca altera o estado diretamente.

## Princípios de arquitetura

- **Tudo in-process.** Sem AWS, sem broker (Kafka/NATS/SQS). Um processo, uma CLI.
- **LLM atrás de interface.** `llm.Provider` (`Name`/`Generate`/`Stream`) com adapters em `internal/llm/local` e `internal/llm/remote`, montados por `internal/llm/factory` (fora de `internal/llm` para evitar ciclo de import).
- **Roteamento com fallback seguro.** `llm.RoutingProvider` escolhe o backend por regra e só cai para o fallback **antes do primeiro chunk** de um stream — nunca mistura duas narrativas.
- **Saída estruturada.** O LLM responde no contrato `[NARRATIVE]` / `[MECHANICS]`; `llm.ParseResponse` converte a parte mecânica em `StructuredEffect` de forma determinística. Linhas inválidas são reportadas e ignoradas.
- **Máquina de turno fixa.** `encounter.TurnMachine`: `AguardandoInput → Resolvendo → Narrando → AplicandoEfeitos → ProximoTurno`, independente do LLM.
- **Eventos in-process.** `events.Dispatcher` síncrono, ordem garantida; pânico de handler vira erro (ADR 0008). O `Engine` publica o que acontece no turno e a memória escuta por ali.

## Estrutura

```
cmd/mestre-virtual/     CLI (stdlib flag)
internal/llm/           Provider, RoutingProvider, ParseResponse
internal/llm/local/     EchoProvider (placeholder determinístico)
internal/llm/remote/    StubProvider (erro explícito até haver SDK)
internal/llm/factory/   monta o Provider a partir da config
internal/knowledge/     base de conhecimento estática (Ref, Item, Class, Environment, NPCTemplate, FileStore)
internal/campaign/      Campaign, Session, log da sessão
internal/character/     Character, PV, inventário
internal/storage/       persistência JSON atômica em data/
internal/encounter/     TurnMachine (fases, iniciativa, Abort/Pass)
internal/events/        Dispatcher
internal/game/          Engine: loop de turno (TurnMachine + LLM + eventos + memória)
internal/memory/        memória de aventura (Store, Recall, Compactor, ContextBuilder, EventRecorder)
internal/obs/           logger slog e decorator LoggingProvider
content/                conteúdo do jogo (JSON)
```

## Uso

```bash
go build -o mestre-virtual ./cmd/mestre-virtual
./mestre-virtual content
./mestre-virtual character create -id aria -name Aria -class guerreiro
./mestre-virtual character list
./mestre-virtual ask -llm routing "abro a porta da cripta"
go test ./...
```

## Jogando (Fases 3–4)

`play` abre (ou cria) uma campanha e entra num REPL. A vez segue a **ordem de iniciativa** (a ordem dos personagens da campanha): cada linha é a ação do personagem da vez, o mestre narra em streaming e o núcleo (`internal/game`) valida os efeitos propostos em `[MECHANICS]` antes de aplicá-los. A seção `[MECHANICS]` nunca é mostrada ao jogador; efeitos aplicados e recusados (com motivo) aparecem logo abaixo da narração.

Cada turno percorre as fases da `encounter.TurnMachine` (`AguardandoInput → Resolvendo → Narrando → AplicandoEfeitos → ProximoTurno`). Personagem com PV 0 é pulado (continua no grupo e volta se for curado). Se o LLM falhar — ou se você apertar Ctrl-C durante a narração — o turno é desfeito: nada é aplicado e a vez continua com o mesmo personagem. A posição da iniciativa e a rodada são salvas na sessão.

```bash
./mestre-virtual character create -id aria -name Aria -class guerreiro
./mestre-virtual character create -id bruno -name Bruno -class mago
# cria a campanha (exige -chars e -env) e abre a sessão 1
./mestre-virtual play -campaign cripta -chars aria,bruno -env entrada-cripta -llm local
aria> abro a porta da cripta
O mestre ouve: Aria (aria): abro a porta da cripta
bruno> /quem
Vez de Bruno (bruno), rodada 1. Iniciativa: aria → bruno
bruno> /passar
Bruno passa a vez.

— Rodada 2 —

aria> /sair
# depois: retoma a sessão ativa (e a vez) ou abre a próxima no último ambiente
./mestre-virtual play -campaign cripta -log-level info
```

Comandos do REPL: `/status` (PV e inventário), `/quem` (de quem é a vez, rodada e iniciativa — não troca o personagem), `/passar` (passa a vez sem agir), `/sair` (encerra a sessão, salva e compacta a memória). Fim da entrada (Ctrl-D) só pausa — o estado é salvo a cada turno. Com `-llm local` o jogo roda offline de ponta a ponta.

Flags úteis: `-memory-budget N` (tokens de memória no prompt; padrão 400, `0` desliga a memória), `-log-level debug|info|warn|error` (padrão `warn`) e `-log-format text|json`. O log vai para **stderr**; a narração, para stdout.

**Eventos publicados pelo `Engine`:** `SessionStarted`, `TurnPhaseChanged` (a cada transição), `PlayerAction`, `NarrationDone`, `EffectApplied`, `EffectRejected`, `SessionEnded`. Ação, narração e efeitos levam `campaign.LogEntry` (a mesma entrada de `Session.Log`) e só são publicados com a narração confirmada, para que log e memória nunca divirjam. Erro de handler é logado e o turno segue.

## Memória (Fase 5)

`internal/memory` dá ao LLM contexto relevante e limitado de sessões anteriores e da atual.

- **Modelo.** Cada `memory.Entry` é um fato, evento ou resumo, com sessão, `Refs` tipadas (`npc:guardiao`, `item:espada-curta`) e importância em [0,1]. Persistência em `data/memory/<campaignID>.json` (escrita atômica, como `internal/storage`); a `Store` é segura para uso concorrente.
- **Recall determinístico e explicável.** `score = 0.50·relevância + 0.25·refs + 0.15·recência + 0.10·importância`, todos em [0,1] e expostos em `Scored.Breakdown`. Relevância é BM25-lite sobre texto normalizado (minúsculas, sem acentos, stopwords, plural leve — "guardiões" casa com "guardião"); recência cai pela metade a cada 3 sessões. Um `Embedder` opcional (ainda sem implementação real) entra com 50% da relevância.
- **Compactação.** `Compactor` troca as entradas de uma sessão encerrada (ou as mais antigas, acima de um limite) por um resumo — via `LLMSummarizer` ou o fallback determinístico `ExtractiveSummarizer`. As originais ficam arquivadas e o resumo guarda seus IDs (`Summarizes`).
- **Prompt.** `ContextBuilder.Build(ctx, query, orçamentoTokens)` escolhe entradas por score até caber no orçamento (tokens ≈ runas/4), sem cortar nenhuma no meio. No jogo, a consulta é a ação do jogador com o ambiente atual e as entidades citadas como `Refs`; o bloco entra na seção "Memória da aventura" do prompt.
- **Eventos.** O `Engine` inscreve um `EventRecorder` no `Dispatcher`: ação, narração e efeitos de cada turno são gravados automaticamente (com `Refs` casadas por nome/ID na base de conhecimento) e a sessão é compactada em `SessionEnded`. A memória é opcional: sem ela o `Engine` funciona igual, só sem a seção no prompt.

## Limitações conhecidas

- A CLI usa só a stdlib (sem Cobra) e não há SDK de LLM real: o ambiente onde o projeto foi iniciado não tinha acesso a `proxy.golang.org`. Os pontos de troca estão documentados no código (`cmd/mestre-virtual`, `internal/llm/remote`).
- `local` é um eco determinístico; `remote` sempre retorna `llm.ErrNotConfigured`.

## Qualidade (Fase 6)

```bash
make ci                 # gofmt -l, go vet, go test -race (o mesmo que o CI)
make fuzz FUZZTIME=1m   # fuzz de llm.ParseResponse
make cover              # cobertura total
```

- Decisões de arquitetura: [`docs/adr/`](docs/adr/README.md).
- CI: `.github/workflows/ci.yml` (vet, gofmt, `test -race`; fuzz curto em push na `main`).
- Logging estruturado (`internal/obs`, `log/slog`) ligado na CLI pelas flags `-log-level` e `-log-format`: o provider é envolvido por `obs.LoggingProvider` e o mesmo logger vai para o `Engine` (falhas de handler, memória indisponível, efeitos recusados). Em código:

```go
logger, _ := obs.NewLogger(obs.Options{Level: slog.LevelInfo, Format: obs.FormatJSON}) // escreve em stderr
p, _ := factory.New(cfg)
p = obs.NewLoggingProvider(p, logger) // obs.WithPromptLogging() para logar o prompt (só em dev)
```

Cada chamada ao LLM gera uma linha com provider, duração, tamanhos de prompt/resposta, chunks, tempo até o primeiro chunk e erro; o conteúdo do prompt só é logado com `WithPromptLogging`.

## Roadmap

| Fase | Escopo | Status |
|---|---|---|
| 0 | Esqueleto, interface `llm.Provider`, factory, CLI | ✅ |
| 1 | Base de conhecimento estruturada (`internal/knowledge`) | ✅ |
| 2 | Campanha, personagem, storage, parser estruturado, máquina de turno, eventos | ✅ |
| 3 | Loop de narração jogável na CLI | ✅ |
| 4 | Integrar turno + eventos + memória + logging ao loop | ✅ |
| 5 | `internal/memory` (memória de aventura) | ✅ |
| 6 | Testes, logging estruturado, ADRs | ✅ |

Cada fase entra por uma branch própria e um pull request, com este README atualizado no mesmo PR.
