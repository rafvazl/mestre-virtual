# 0007 — Persistência em arquivos JSON com escrita atômica

- Status: aceito
- Data: 2026-10-18

## Contexto

O estado de execução (campanhas, sessões, personagens) precisa sobreviver
entre execuções da CLI. O volume é pequeno (dezenas de registros), há um
único processo escrevendo e é útil poder ler/editar o estado à mão e
versioná-lo. O ambiente não permite adicionar drivers de banco.

## Decisão

`storage.FileStore` grava cada registro em `data/<tipo>/<id>.json`
(JSON indentado). A escrita é atômica: grava num temporário `.tmp-*` no
mesmo diretório e faz `os.Rename` sobre o destino, de modo que um leitor
vê o arquivo antigo ou o novo, nunca um parcial. IDs são validados por
regex (`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`), o que impede path traversal.
A listagem ignora temporários, arquivos que não são `.json` e nomes que
não são ids válidos.

## Alternativas consideradas

- **SQLite**: transações, consultas e concorrência; exige driver (cgo ou
  dependência pura Go) — indisponível no ambiente.
- **Um único arquivo com todo o estado**: simples, mas reescreve tudo a
  cada save e um erro corrompe tudo.
- **bbolt/Badger**: mesmo problema de dependência e o estado deixa de ser
  legível à mão.

## Consequências

- Positivas: zero dependências, estado legível e diffável, testes com
  `t.TempDir()`.
- Negativas e limites conhecidos (honestos):
  - **Sem `fsync`**: o rename é atômico em relação a outros processos, mas
    uma queda de energia logo após o save pode perder a última escrita (ou,
    em alguns sistemas de arquivos, deixar o arquivo vazio). Para uma CLI
    de jogo o risco foi aceito; corrigir é chamar `Sync` no temporário e no
    diretório.
  - Sem trava entre processos: duas CLIs salvando o mesmo personagem
    resultam em "última escrita vence".
  - Sem transação entre registros: salvar campanha e personagem não é
    atômico como um todo.
  - Sem versão de schema nos arquivos; mudanças de formato vão exigir
    migração explícita.
  - Temporários órfãos (`.tmp-*`) podem sobrar se o processo morrer entre
    a criação e o rename; são ignorados pela listagem, mas não limpos.
- Revisar quando houver modo servidor/multiusuário ou volume que exija
  consultas.
