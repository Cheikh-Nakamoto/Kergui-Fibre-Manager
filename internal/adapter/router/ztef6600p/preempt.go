package ztef6600p

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// The box keeps one admin session. Seen live on 2026-11-15 (fw ZTEGF660006SN):
// a login while another session is open is accepted, but the new session is
// parked. The main page then shows "Un autre utilisateur est en train de
// configurer l'appareil" with a radio per open session, every menuView answers
// 404 and every menuData answers SessionTimeout, until the client POSTs
// action=preempt to login_preempt. The open session is very often our own
// previous one: each request logs in again and nothing ever logs out.
const preemptURL = "/?_type=loginData&_tag=login_preempt"

var (
	preemptSessRe  = regexp.MustCompile(`name="preempt_sessid"\s+value="?([^"\s/>]+)`)
	preemptLabelRe = regexp.MustCompile(`<label for="radio1">([^<]*)</label>`)
)

func isPreemptPage(page string) bool {
	return strings.Contains(page, `name="preempt_sessid"`)
}

// preempt evicts the session the box names on its preempt page and returns the
// main page of the session that is now live.
func (g *Gateway) preempt(ctx context.Context, page string) (string, error) {
	m := preemptSessRe.FindStringSubmatch(page)
	if m == nil {
		return "", fmt.Errorf("%w: another session holds the router and its id was not found", domain.ErrAuthFailed)
	}
	who := "unknown"
	if l := preemptLabelRe.FindStringSubmatch(page); l != nil {
		who = html.UnescapeString(l[1])
	}
	g.log.Warnf("f6600p: another session holds the router (%s), taking it over", who)

	form := url.Values{"action": {"preempt"}, "preempt_sessid": {m[1]}}.Encode()
	if _, status, err := g.sess.PostRaw(ctx, preemptURL, form); err != nil {
		return "", fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	} else if status != 200 {
		return "", fmt.Errorf("%w: login_preempt returned %d", domain.ErrAuthFailed, status)
	}

	body, _, err := g.sess.Get(ctx, "/")
	if err != nil {
		return "", fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if isPreemptPage(string(body)) {
		return "", fmt.Errorf("%w: the router kept the other session (%s) after preempt", domain.ErrAuthFailed, who)
	}
	return string(body), nil
}
