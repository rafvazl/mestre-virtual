package llm

import (
	"context"
	"fmt"
)

// Rule associa um predicado sobre a Request a um Provider.
type Rule struct {
	Name     string
	Match    func(Request) bool
	Provider Provider
}

// RoutingProvider escolhe um backend por regra (ex.: diálogo de NPC no
// modelo local, narração de clímax no remoto) e, opcionalmente, cai para um
// Fallback quando o escolhido falha.
//
// Invariante de Stream: o fallback só é tentado ANTES do primeiro chunk com
// texto ser entregue ao consumidor. Depois disso, trocar de backend
// misturaria duas narrativas diferentes; o erro é propagado como está.
type RoutingProvider struct {
	Rules    []Rule
	Default  Provider
	Fallback Provider
}

// Name implementa Provider.
func (r *RoutingProvider) Name() string { return "routing" }

// Route devolve o provider escolhido para a requisição.
func (r *RoutingProvider) Route(req Request) (Provider, error) {
	for _, rule := range r.Rules {
		if rule.Match != nil && rule.Match(req) && rule.Provider != nil {
			return rule.Provider, nil
		}
	}
	if r.Default != nil {
		return r.Default, nil
	}
	return nil, ErrNoRoute
}

func (r *RoutingProvider) fallbackFor(primary Provider) Provider {
	if r.Fallback == nil || r.Fallback == primary {
		return nil
	}
	return r.Fallback
}

// Generate implementa Provider.
func (r *RoutingProvider) Generate(ctx context.Context, req Request) (Response, error) {
	p, err := r.Route(req)
	if err != nil {
		return Response{}, err
	}
	resp, err := p.Generate(ctx, req)
	if err == nil {
		return resp, nil
	}
	fb := r.fallbackFor(p)
	if fb == nil || ctx.Err() != nil {
		return Response{}, err
	}
	resp, fbErr := fb.Generate(ctx, req)
	if fbErr != nil {
		return Response{}, fmt.Errorf("primário %s: %v; fallback %s: %w", p.Name(), err, fb.Name(), fbErr)
	}
	return resp, nil
}

// Stream implementa Provider respeitando a invariante do primeiro chunk.
func (r *RoutingProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	p, err := r.Route(req)
	if err != nil {
		return nil, err
	}
	fb := r.fallbackFor(p)

	src, err := p.Stream(ctx, req)
	if err != nil {
		if fb == nil {
			return nil, err
		}
		return fb.Stream(ctx, req)
	}
	if fb == nil {
		return src, nil
	}

	out := make(chan Chunk)
	go func() {
		defer close(out)
		emitted := false
		for c := range src {
			if c.Err != nil && !emitted && ctx.Err() == nil {
				// Falhou antes de qualquer texto: troca para o fallback.
				fbSrc, fbErr := fb.Stream(ctx, req)
				if fbErr != nil {
					send(ctx, out, Chunk{Err: fmt.Errorf("primário %s: %v; fallback %s: %w", p.Name(), c.Err, fb.Name(), fbErr)})
					return
				}
				for fc := range fbSrc {
					if !send(ctx, out, fc) {
						return
					}
				}
				return
			}
			if c.Text != "" {
				emitted = true
			}
			if !send(ctx, out, c) {
				return
			}
		}
	}()
	return out, nil
}

// send entrega o chunk ou desiste se o contexto for cancelado.
func send(ctx context.Context, out chan<- Chunk, c Chunk) bool {
	select {
	case out <- c:
		return true
	case <-ctx.Done():
		return false
	}
}
