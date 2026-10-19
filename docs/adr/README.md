# Architecture Decision Records

Decisões de arquitetura do `mestre-virtual`, no formato curto de Nygard/MADR
(Contexto, Decisão, Alternativas consideradas, Consequências). Uma ADR
aceita não é editada para mudar de ideia: cria-se uma nova que a substitui
e o status da antiga passa a "substituída por NNNN".

| # | Decisão | Status |
|---|---|---|
| [0001](0001-tudo-in-process-sem-broker.md) | Tudo in-process, sem broker nem nuvem | aceito |
| [0002](0002-abstracao-llm-provider-e-factory.md) | Abstração `llm.Provider` e factory fora do pacote | aceito |
| [0003](0003-roteamento-local-remoto-primeiro-chunk.md) | Roteamento local/remoto e invariante do primeiro chunk | aceito |
| [0004](0004-saida-estruturada-parser-deterministico.md) | Contrato de saída estruturada e parser determinístico | aceito |
| [0005](0005-maquina-de-turno-independente-do-llm.md) | Máquina de turno independente do LLM | aceito |
| [0006](0006-cli-stdlib-flag-sem-sdks.md) | CLI com `flag` da stdlib e sem SDKs | aceito (restrição de ambiente) |
| [0007](0007-persistencia-json-escrita-atomica.md) | Persistência em arquivos JSON com escrita atômica | aceito |
| [0008](0008-recover-em-handlers-de-eventos.md) | `recover` nos handlers do `events.Dispatcher` | aceito |

## Modelo

```markdown
# NNNN — Título

- Status: proposto | aceito | substituído por NNNN
- Data: AAAA-MM-DD

## Contexto
## Decisão
## Alternativas consideradas
## Consequências
```
