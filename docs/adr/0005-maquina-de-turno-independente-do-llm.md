# 0005 — Máquina de turno independente do LLM

- Status: aceito
- Data: 2026-10-18

## Contexto

Um encontro tem ordem de iniciativa e etapas bem definidas. Se o fluxo do
turno dependesse do que o LLM escreve ("agora é a vez do goblin"), o jogo
pularia jogadores, repetiria turnos ou aplicaria efeitos fora de hora.

## Decisão

`encounter.TurnMachine` é uma máquina de estados fixa:

`AguardandoInput → Resolvendo → Narrando → AplicandoEfeitos → ProximoTurno → AguardandoInput`

Só a transição para a próxima fase é válida (`ErrInvalidTransition` caso
contrário). Ao sair de `ProximoTurno` a vez passa ao próximo da
iniciativa e a rodada incrementa ao dar a volta. Hooks (`OnTransition`)
permitem publicar eventos sem acoplar a máquina ao `Dispatcher`. O LLM só
participa da fase `Narrando`; efeitos são aplicados em `AplicandoEfeitos`.

## Alternativas consideradas

- **Deixar o LLM conduzir o turno**: mais "livre", mas não determinístico e
  impossível de testar.
- **Máquina genérica configurável (tabela de transições externa)**: mais
  flexível, mas não há hoje necessidade de fluxos alternativos.
- **Sem máquina, só um loop sequencial**: funciona para um turno, mas não
  dá um lugar único para validar ordem, emitir eventos e retomar o estado.

## Consequências

- Positivas: ordem garantida e testável; como efeitos só são aplicados
  depois de `Narrando`, uma falha do LLM nessa fase acontece antes de
  qualquer mudança de estado.
- Negativas: não há transições de exceção (abortar/repetir o turno quando o
  LLM falha, fuga, morte de participante, reação fora de turno); hoje o
  loop teria de avançar pelas fases mesmo sem efeitos. Entrarão como
  evolução explícita. A ordem de iniciativa é fixa após a criação (não há
  remover/adicionar participante).
- A máquina não é segura para uso concorrente; pressupõe o loop
  single-threaded da CLI.
