# 0004 — Contrato de saída estruturada e parser determinístico

- Status: aceito
- Data: 2026-10-18

## Contexto

O LLM precisa narrar (texto livre) e também indicar consequências
mecânicas (dano, itens). Se o LLM alterasse o estado diretamente, um texto
alucinado viraria PV perdido ou item inexistente no inventário, e as
regras dependeriam de um componente não determinístico.

## Decisão

O LLM responde no contrato:

```
[NARRATIVE]
texto livre
[MECHANICS]
HP_CHANGE target=<id> amount=<int>
ITEM_GAINED target=<id> item=<id> [qty=<int>]
ITEM_LOST target=<id> item=<id> [qty=<int>]
```

`llm.ParseResponse` interpreta isso de forma determinística e devolve
`StructuredEffect`s **propostos**. Linhas inválidas viram `ParseError`
(com número da linha) e são ignoradas. Sem marcadores, tudo é narrativa
(degradação segura). O LLM nunca altera estado: quem valida o alvo, o item
na base de conhecimento e aplica é o núcleo em Go.

## Alternativas consideradas

- **JSON mode / tool calling**: mais robusto sintaticamente, mas depende de
  recursos do provider (indisponíveis no eco local e em muitos modelos
  locais pequenos) e piora o streaming da narração.
- **Extrair efeitos da narrativa com regex/NLP**: frágil e ambíguo.
- **Pedir ao LLM o estado completo após o turno**: o LLM vira fonte de
  verdade do estado — exatamente o que se quer evitar.

## Consequências

- Positivas: parser puro, testável, coberto por fuzz (nunca entra em
  pânico; efeitos sempre têm target; itens sempre têm id e quantidade
  > 0). Uma resposta ruim do LLM degrada para "só narração".
- Negativas: o contrato é rígido; modelos fracos erram o formato e os
  efeitos se perdem (são reportados, não aplicados). Chaves desconhecidas
  são aceitas e ignoradas; chave repetida fica com o último valor.
- `amount` aceita qualquer `int`, inclusive valores absurdos; a Fase 6
  corrigiu o overflow em `character`, mas limites de jogo (ex.: dano máximo
  por turno) ainda não existem e devem ficar na camada de regras, não no
  parser.
- Validação semântica (target existe? item existe em `knowledge`?) não é
  responsabilidade do parser e precisa ser feita pelo loop de jogo.
