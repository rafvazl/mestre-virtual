# 0001 — Tudo in-process, sem broker nem nuvem

- Status: aceito
- Data: 2026-10-18

## Contexto

O mestre virtual é uma CLI usada por uma mesa de jogo: um processo, um
usuário digitando, latência dominada pelo LLM. A ordem dos eventos importa
para a narrativa (o golpe acontece antes do PV cair). O projeto também é
estudo de Go, e o ambiente de desenvolvimento não tem acesso a serviços de
nuvem nem a `proxy.golang.org`.

## Decisão

Todo o sistema roda num único processo. Comunicação entre componentes é
chamada de função; eventos passam por `events.Dispatcher`, síncrono, com
handlers executados na goroutine de quem publica e na ordem de inscrição.
Não há AWS, Kafka, NATS, SQS nem banco de dados.

## Alternativas consideradas

- **Broker (NATS/Kafka/SQS)**: dá desacoplamento temporal e escala
  horizontal, que não são necessidades de uma CLI de um jogador; traz
  operação, serialização, entrega "pelo menos uma vez" e reordenação, que
  teriam de ser tratadas para manter a narrativa coerente.
- **Canais Go assíncronos como barramento**: simples, mas perde a garantia
  de ordem entre publicadores e torna erros de handler difíceis de
  propagar para quem publicou.
- **Serviços separados (API + worker de LLM)**: preparam para multiusuário,
  mas antecipam um problema que ainda não existe.

## Consequências

- Positivas: depuração trivial (stack única), testes sem infraestrutura,
  ordem determinística, erros de handler voltam agregados em `Publish`.
- Negativas: um handler lento bloqueia quem publica; um handler que entra
  em pânico derruba a publicação (não há `recover`). Não há durabilidade de
  eventos: se o processo cair, o que não foi salvo em `storage` se perde.
- Se surgir modo multiusuário ou servidor, `Dispatcher` é o ponto de
  troca: a interface `Publish/Subscribe` pode ganhar uma implementação
  sobre broker sem mudar os produtores.
