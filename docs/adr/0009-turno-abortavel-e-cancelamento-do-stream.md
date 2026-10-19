# 0009 — Turno abortável, passar a vez e cancelamento do stream

- Status: aceito
- Data: 2026-10-19

## Contexto

Na Fase 4 o `Engine` passou a ser dirigido pela `encounter.TurnMachine`
(ADR 0005). Três situações não cabiam no ciclo fixo: o LLM falha (ou o
jogador cancela) no meio da narração; o jogador quer passar a vez; e o
REPL antigo deixava trocar o ator livremente com `/quem <id>`, o que
contradiz uma ordem de iniciativa. Além disso, o contrato de
`llm.Provider.Stream` não dizia como tratar um stream que fecha "limpo"
depois de o contexto ser cancelado.

## Decisão

- **Duas transições de exceção explícitas** na `TurnMachine`:
  `Abort` (`Resolvendo|Narrando → AguardandoInput`, mesmo ator) e `Pass`
  (`AguardandoInput → ProximoTurno`). Abortar depois de `AplicandoEfeitos`
  é inválido, porque o estado já pode ter mudado.
- **Cancelamento do stream**: o provider fecha o canal logo ao ver `ctx`
  cancelado; o consumidor drena o canal e, ao final, checa `ctx.Err()`.
  Stream truncado é falha de geração: o `Engine` aborta o turno sem aplicar,
  registrar ou publicar nada além das mudanças de fase. Na CLI, Ctrl-C
  durante a narração cancela só o turno.
- **`/quem` só consulta** (vez, rodada e iniciativa); `/passar` passa a vez.
  Personagem com PV 0 é pulado, mas continua na iniciativa e volta a jogar
  se for curado. A posição (ator e rodada) é salva em `Session.Turn`
  (opcional no JSON) e cada sessão nova recomeça na rodada 1.

## Alternativas consideradas

- **Percorrer todas as fases ao passar a vez / ao falhar**: mantém a tabela
  de transições intacta, mas publica fases (`Narrando`, `AplicandoEfeitos`)
  que não aconteceram.
- **Confiar no `Chunk{Err}` do provider**: nem todo adapter o envia ao ser
  cancelado (o envio pode bloquear); checar `ctx.Err()` no consumidor é a
  única garantia.
- **Manter `/quem <id>` trocando o ator**: flexível para mesa "livre", mas
  torna a iniciativa decorativa.

## Consequências

- Positivas: falha do LLM nunca consome o turno; eventos refletem só o que
  aconteceu; sessão retomada continua de quem era a vez.
- Negativas: não há ainda reação fora de turno, NPCs na iniciativa nem
  reordenação; com todos os personagens em PV 0, o jogo segue com o
  próximo da ordem (não há "fim de jogo").
