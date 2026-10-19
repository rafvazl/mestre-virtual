# 0003 — Roteamento local/remoto e invariante do primeiro chunk

- Status: aceito
- Data: 2026-10-18

## Contexto

Tipos de pedido diferentes cabem em modelos diferentes: diálogo curto de
NPC pode ir para um modelo local barato; narração de clímax, para um modelo
remoto melhor. Backends falham (rede, cota, modelo não carregado), e o
jogo deve continuar quando houver alternativa. Com streaming, porém, o
jogador já está lendo o texto enquanto ele chega.

## Decisão

`llm.RoutingProvider` escolhe o backend pela primeira `Rule` cujo `Match`
aceita a `Request` (tipicamente por `Tags`), senão usa `Default`. Se houver
`Fallback` distinto do escolhido:

- `Generate`: falhou e o contexto não foi cancelado → tenta o fallback.
- `Stream`: o fallback só é tentado **antes do primeiro chunk com texto**
  ser entregue ao consumidor (erro ao iniciar ou erro como primeiro
  chunk). Depois disso, o erro é propagado como está — nunca se troca de
  backend no meio de uma narração.
- Contexto cancelado nunca dispara fallback (corrigido em `Stream` na
  Fase 6; `Generate` já se comportava assim).

## Alternativas consideradas

- **Retentar do zero no fallback após falha no meio do stream**: o jogador
  veria duas narrativas diferentes coladas; reescrever a tela exige que a
  UI saiba "desfazer" texto.
- **Bufferizar a resposta inteira antes de mostrar**: permite fallback em
  qualquer ponto, mas elimina o benefício do streaming.
- **Continuar no fallback a partir do texto já emitido** (prompt de
  continuação): tecnicamente possível, mas a voz e o estilo mudam no meio e
  o resultado é imprevisível; fica como evolução possível.
- **Circuit breaker/health check**: útil com tráfego real; prematuro para
  uma CLI.

## Consequências

- Positivas: falha antes do primeiro token é invisível ao jogador; a
  narrativa nunca mistura dois modelos.
- Negativas: uma falha no meio do stream entrega texto truncado e um erro;
  cabe ao loop de jogo decidir o que fazer (ex.: descartar o turno, que
  ainda não aplicou efeitos — ver ADR 0005).
- Um stream cancelado pode terminar com o canal fechado **sem** chunk de
  erro (o envio do erro é "melhor esforço"); o consumidor deve checar
  `ctx.Err()` após o fim do canal para distinguir fim normal de
  cancelamento.
- Chunks vazios (`Text == ""`) não contam como "primeiro chunk".
- Regras são avaliadas em ordem e sem prioridade explícita; com muitas
  regras isso fica frágil.
