package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/rafvazl/mestre-virtual/internal/game"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/llm/factory"
	"github.com/rafvazl/mestre-virtual/internal/storage"
)

const playHelp = `Comandos: /status (PV e inventário) · /quem (de quem é a vez) · /passar (passa a vez) · /sair (encerra a sessão)
Qualquer outra linha é a ação do personagem da vez; a vez segue a ordem de iniciativa (personagens com PV 0 são pulados).
Ctrl-C durante a narração cancela o turno sem aplicar nada.`

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

	ctx := context.Background()
	sc := bufio.NewScanner(in)
	round := eng.Round()
	for {
		if r := eng.Round(); r != round {
			round = r
			fmt.Fprintf(out, "\n— Rodada %d —\n", r)
		}
		fmt.Fprintf(out, "\n%s> ", eng.Current())
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
		case line == "/quem":
			writeTurn(out, eng)
		case strings.HasPrefix(line, "/quem "):
			fmt.Fprintln(out, "a vez segue a ordem de iniciativa e não pode ser trocada; use /passar para passar a vez")
			writeTurn(out, eng)
		case line == "/passar":
			who, err := eng.Pass()
			if err != nil {
				return err
			}
			c, _ := eng.Character(who)
			fmt.Fprintf(out, "%s passa a vez.\n", c.Name)
		case strings.HasPrefix(line, "/"):
			fmt.Fprintf(out, "comando desconhecido %q\n%s\n", line, playHelp)
		default:
			res, err := playTurn(ctx, eng, line)
			printTurn(out, eng, res)
			if err != nil {
				if errors.Is(err, game.ErrGeneration) {
					fmt.Fprintf(out, "erro: o mestre não conseguiu narrar (%v). Nada foi aplicado; a vez continua com %s.\n", err, eng.Current())
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

// playTurn joga um turno com Ctrl-C (SIGINT) ligado ao contexto: durante a
// narração, Ctrl-C cancela só o turno. Fora dele, o sinal volta ao
// comportamento padrão (encerrar o processo).
func playTurn(ctx context.Context, eng *game.Engine, line string) (game.TurnResult, error) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	return eng.PlayTurn(ctx, line)
}

// writeTurn mostra de quem é a vez, a rodada e a ordem de iniciativa.
func writeTurn(out io.Writer, eng *game.Engine) {
	cur, _ := eng.Character(eng.Current())
	order := make([]string, 0, len(eng.Party()))
	for _, c := range eng.Party() {
		s := c.ID
		if !c.Alive() {
			s += " (PV 0, pulado)"
		}
		order = append(order, s)
	}
	fmt.Fprintf(out, "Vez de %s (%s), rodada %d. Iniciativa: %s\n", cur.Name, cur.ID, eng.Round(), strings.Join(order, " → "))
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
