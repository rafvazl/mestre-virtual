# 0008 — `recover` nos handlers do `events.Dispatcher`

- Status: aceito
- Data: 2026-10-19

## Contexto

Na Fase 4 o `Engine` publica eventos no meio do turno (`PlayerAction`,
`EffectApplied`...) e a memória (`memory.EventRecorder`) passa a ser um
handler. O `Dispatcher` é síncrono: um pânico num handler subiria pela pilha
do `Engine.PlayTurn` e derrubaria a CLI no meio de um turno — depois de
efeitos aplicados em memória e antes de persistir. Um handler é código
acessório (memória, log, métricas); não deve conseguir corromper o turno.

## Decisão

`Dispatcher.Publish` chama cada handler sob `recover`. Um pânico vira um
erro que envolve `events.ErrHandlerPanic` (com tipo do evento, valor do
pânico e pilha) e entra no erro agregado (`errors.Join`). Os handlers
seguintes continuam recebendo o evento. Quem publica (o `Engine`) loga o
erro e segue o turno: falha de handler nunca interrompe o jogo.

## Alternativas consideradas

- **Deixar o pânico propagar**: falha barulhenta e honesta, mas um bug na
  memória derrubaria a sessão de jogo.
- **`recover` no `Engine`**: protegeria só um chamador e interromperia os
  handlers restantes do mesmo evento.
- **Handlers assíncronos isolados (goroutine por handler)**: perde a ordem
  garantida, que é o motivo do `Dispatcher` síncrono (ADR 0001).

## Consequências

- Positivas: o turno é atômico em relação a handlers; o pânico fica visível
  no log com a pilha.
- Negativas: um handler em estado inconsistente após o pânico continua
  inscrito e recebe os próximos eventos; bugs podem passar despercebidos se
  ninguém olhar o log (o nível usado pelo `Engine` é `warn`, visível no
  padrão da CLI).
