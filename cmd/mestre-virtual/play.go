package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rafvazl/mestre-virtual/internal/game"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/llm/factory"
	"github.com/rafvazl/mestre-virtual/internal/storage"
)

const playHelp = `Comandos: /status (PV e inventário) · /quem <id> (troca o personagem ativo) · /sair (encerra a sessão)
Qualquer outra linha é a ação do personagem ativo.`

// cmdPlay abre (ou cria) a campanha e roda o REPL de jogo lendo linhas de in.
//
// /sair encerra a sessão. Fim da entrada (EOF/Ctrl-D) apenas pausa: o estado
// já é salvo a cada turno e a sessão continua aberta para ser retomada.
func cmdPlay(args []string, in io.Reader, out io.Writer) error {
	var g globals
	fs := newFlagSet("play", &g)
	campID := fs.String("campaign", "", "ID da campanha (obrigatório)")
	chars := fs.String("chars", "", "IDs dos personagens separados por vírgula (obrigatório ao criar a campanha)")
	env := fs.String("env", "", "ID do ambiente inicial (obrigatório ao criar a campanha)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *campID == "" {
		return errors.New("-campaign é obrigatório")
	}
	ks, err := knowledge.NewFileStore(g.contentDir)
	if err != nil {
		return err
	}
	st, err := storage.NewFileStore(g.dataDir)
	if err != nil {
		return err
	}
	prov, err := factory.New(factory.Config{Backend: g.backend, RemoteModel: g.model})
	if err != nil {
		return err
	}
	eng, err := game.Open(game.Deps{Knowledge: ks, Campaigns: st, Characters: st, Provider: prov, Out: out},
		game.Config{}, game.OpenOptions{CampaignID: *campID, CharacterIDs: splitList(*chars), EnvironmentID: *env})
	if err != nil {
		return err
	}

	envInfo, err := eng.Environment()
	if err != nil {
		return err
	}
	verb := "Iniciando"
	if eng.Resumed {
		verb = "Retomando"
	}
	fmt.Fprintf(out, "%s a sessão %d da campanha %q em %s (LLM: %s).\n%s\n\n%s\n",
		verb, eng.Session().Number, eng.Campaign().ID, envInfo.Name, prov.Name(), envInfo.Description, playHelp)

	active := eng.Party()[0].ID
	ctx := context.Background()
	sc := bufio.NewScanner(in)
	for {
		fmt.Fprintf(out, "\n%s> ", active)
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case line == "/sair":
			if err := eng.End(); err != nil {
				return err
			}
			fmt.Fprintln(out, "Sessão encerrada e salva.")
			return nil
		case line == "/status":
			eng.WriteStatus(out)
		case line == "/quem" || strings.HasPrefix(line, "/quem "):
			id := strings.TrimSpace(strings.TrimPrefix(line, "/quem"))
			c, ok := eng.Character(id)
			if !ok {
				fmt.Fprintf(out, "personagem %q não está na campanha (grupo: %s)\n", id, partyIDs(eng))
				continue
			}
			active = c.ID
			fmt.Fprintf(out, "Agora jogando com %s.\n", c.Name)
		case strings.HasPrefix(line, "/"):
			fmt.Fprintf(out, "comando desconhecido %q\n%s\n", line, playHelp)
		default:
			res, err := eng.PlayTurn(ctx, active, line)
			printTurn(out, eng, res)
			if err != nil {
				if errors.Is(err, game.ErrGeneration) {
					fmt.Fprintf(out, "erro: o mestre não conseguiu narrar (%v). Nada foi aplicado; tente de novo.\n", err)
					continue
				}
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	fmt.Fprintln(out, "\nEntrada encerrada; sessão pausada (use /sair para encerrá-la).")
	return nil
}

// printTurn mostra os efeitos do turno (a narrativa já saiu em streaming).
func printTurn(out io.Writer, eng *game.Engine, res game.TurnResult) {
	for _, a := range res.Applied {
		fmt.Fprintf(out, "  * %s\n", eng.DescribeApplied(a))
	}
	for _, r := range res.Rejected {
		fmt.Fprintf(out, "  ! efeito ignorado: %s (%s)\n", game.FormatEffect(r.Effect), r.Reason)
	}
	for _, e := range res.ParseErrors {
		fmt.Fprintf(out, "  ! linha ignorada: %v\n", e)
	}
}

func partyIDs(eng *game.Engine) string {
	ids := make([]string, 0, len(eng.Party()))
	for _, c := range eng.Party() {
		ids = append(ids, c.ID)
	}
	return strings.Join(ids, ", ")
}

// splitList converte "a, b,,c" em [a b c].
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
