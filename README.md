# mestre-virtual

Mestre virtual de RPG de mesa em Go: uma CLI que narra a aventura, aplica regras e controla NPCs. A geração de texto vem de um LLM (local ou remoto); **estado, regras e memória ficam no núcleo em Go** — o LLM nunca altera o estado diretamente.

## Princípios de arquitetura

- **Tudo in-process.** Sem AWS, sem broker (Kafka/NATS/SQS). Um processo, uma CLI.
- **LLM atrás de interface.** `llm.Provider` (`Name`/`Generate`/`Stream`) com adapters em `internal/llm/local` e `internal/llm/remote`, montados por `internal/llm/factory` (fora de `internal/llm` para evitar ciclo de import).
- **Roteamento com fallback seguro.** `llm.RoutingProvider` escolhe o backend por regra e só cai para o fallback **antes do primeiro chunk** de um stream — nunca mistura duas narrativas.
- **Saída estruturada.** O LLM responde no contrato `[NARRATIVE]` / `[MECHANICS]`; `llm.ParseResponse` converte a parte mecânica em `StructuredEffect` de forma determinística. Linhas inválidas são reportadas e ignoradas.
- **Máquina de turno fixa.** `encounter.TurnMachine`: `AguardandoInput → Resolvendo → Narrando → AplicandoEfeitos → ProximoTurno`, independente do LLM.
- **Eventos in-process.** `events.Dispatcher` síncrono, ordem garantida.

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
internal/encounter/     TurnMachine
internal/events/        Dispatcher
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

## Jogando (Fase 3)

`play` abre (ou cria) uma campanha e entra num REPL: cada linha é a ação do personagem ativo; o mestre narra em streaming e o núcleo (`internal/game`) valida os efeitos propostos em `[MECHANICS]` antes de aplicá-los. A seção `[MECHANICS]` nunca é mostrada ao jogador; efeitos aplicados e recusados (com motivo) aparecem logo abaixo da narração.

```bash
./mestre-virtual character create -id aria -name Aria -class guerreiro
./mestre-virtual character create -id bruno -name Bruno -class mago
# cria a campanha (exige -chars e -env) e abre a sessão 1
./mestre-virtual play -campaign cripta -chars aria,bruno -env entrada-cripta -llm local
aria> abro a porta da cripta
O mestre ouve: Aria (aria): abro a porta da cripta
aria> /quem bruno
bruno> /status
bruno> /sair
# depois: retoma a sessão ativa ou abre a próxima no último ambiente
./mestre-virtual play -campaign cripta
```

Comandos do REPL: `/status` (PV e inventário), `/quem <id>` (troca o personagem ativo), `/sair` (encerra a sessão e salva). Fim da entrada (Ctrl-D) só pausa — o estado é salvo a cada turno. Com `-llm local` o jogo roda offline de ponta a ponta.

## Limitações conhecidas

- A CLI usa só a stdlib (sem Cobra) e não há SDK de LLM real: o ambiente onde o projeto foi iniciado não tinha acesso a `proxy.golang.org`. Os pontos de troca estão documentados no código (`cmd/mestre-virtual`, `internal/llm/remote`).
- `local` é um eco determinístico; `remote` sempre retorna `llm.ErrNotConfigured`.

## Roadmap

| Fase | Escopo | Status |
|---|---|---|
| 0 | Esqueleto, interface `llm.Provider`, factory, CLI | ✅ |
| 1 | Base de conhecimento estruturada (`internal/knowledge`) | ✅ |
| 2 | Campanha, personagem, storage, parser estruturado, máquina de turno, eventos | ✅ |
| 3 | Loop de narração jogável na CLI | ✅ |
| 4 | Integrar turno + eventos ao loop | ⏳ |
| 5 | `internal/memory` (memória de aventura) | ⏳ |
| 6 | Testes, logging estruturado, ADRs | ⏳ |

Cada fase entra por uma branch própria e um pull request, com este README atualizado no mesmo PR.
