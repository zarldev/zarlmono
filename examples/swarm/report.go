package main

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	boardtools "github.com/zarldev/zarlmono/zkit/agent/tools/board"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Report records protocol evidence, not model quality or real token cost.
// Activations and response counts can vary with concurrent delivery timing.
type Report struct {
	Mode                   string         `json:"mode"`
	Complete               bool           `json:"complete"`
	InitiallyIdle          bool           `json:"initially_idle"`
	Handoffs               int            `json:"handoffs"`
	DuplicateContributions int            `json:"duplicate_contributions"`
	ShutdownJoined         bool           `json:"shutdown_joined"`
	TokenUsage             string         `json:"token_usage"`
	Members                []MemberReport `json:"members"`
	Findings               []Finding      `json:"findings"`
}

// MemberReport counts retained history after the member has stopped. Responses
// count recorded assistant messages, not failed or unrecorded provider attempts.
type MemberReport struct {
	Role               swarm.AgentID `json:"role"`
	Activations        int           `json:"activations"`
	Reactivations      int           `json:"reactivations"`
	Responses          int           `json:"responses"`
	ReadCalls          int           `json:"read_calls"`
	PostCalls          int           `json:"post_calls"`
	Observations       int           `json:"observations"`
	InitialAssignments int           `json:"initial_assignments"`
	Pending            int           `json:"pending"`
	Stopped            bool          `json:"stopped"`
}

// Finding omits timestamps so the final evidence chain is reproducible.
type Finding struct {
	ID      board.ID     `json:"id"`
	Author  board.Author `json:"author"`
	Topic   string       `json:"topic"`
	ReplyTo board.ID     `json:"reply_to"`
	Body    string       `json:"body"`
}

func author(role swarm.AgentID) board.Author {
	if role == "host" {
		return "host"
	}
	return board.Author("agent/" + role)
}

func collectReport(ctx context.Context, goal *swarm.Swarm, member swarm.Member, steps []contribution, report *Report) error {
	report.Mode = "scripted"
	report.TokenUsage = "not reported by scripted clients; no live API calls"
	for _, step := range steps[1:] {
		snapshot, err := goal.Snapshot(step.role)
		if err != nil {
			return fmt.Errorf("collect %s: %w", step.role, err)
		}
		report.Members = append(report.Members, memberReport(snapshot))
	}
	page, err := member.List(ctx, 0, board.MaxPage, "")
	if err != nil {
		return fmt.Errorf("collect board: %w", err)
	}
	for _, summary := range page.Posts {
		posts, err := member.Read(ctx, []board.ID{summary.ID})
		if err != nil {
			return fmt.Errorf("collect post %d: %w", summary.ID, err)
		}
		post := posts[0]
		report.Findings = append(report.Findings, Finding{ID: post.ID, Author: post.Author, Topic: post.Topic, ReplyTo: post.ReplyTo, Body: post.Body})
	}
	assessFindings(steps, report)
	return nil
}

func memberReport(snapshot swarm.Snapshot) MemberReport {
	report := MemberReport{Role: snapshot.ID, Activations: snapshot.Activations,
		Reactivations: max(0, snapshot.Activations-1), Pending: snapshot.Pending, Stopped: snapshot.State == swarm.States.STOPPED}
	for _, message := range snapshot.Messages {
		if message.Observation.Version != 0 {
			report.Observations++
		} else if message.Role == llm.RoleUser {
			report.InitialAssignments++
		}
		if message.Role == llm.RoleAssistant {
			report.Responses++
		}
		for _, call := range message.ToolCalls {
			switch call.Function.Name {
			case boardtools.ReadName.String():
				report.ReadCalls++
			case boardtools.PostName.String():
				report.PostCalls++
			}
		}
	}
	return report
}

func assessFindings(steps []contribution, report *Report) {
	var previous board.ID
	chain := true
	for index, step := range steps {
		count := 0
		for _, finding := range report.Findings {
			if finding.Author != author(step.role) || finding.Topic != step.topic {
				continue
			}
			count++
			if chain && finding.Body == step.body && finding.ReplyTo == previous && finding.ID > previous {
				previous = finding.ID
				if index > 0 {
					report.Handoffs++
				}
			} else {
				chain = false
			}
		}
		report.DuplicateContributions += max(0, count-1)
		chain = chain && count == 1
	}
	report.Complete = chain && report.InitiallyIdle && len(report.Findings) == len(steps)
	for _, member := range report.Members {
		report.Complete = report.Complete && member.Stopped && member.Pending == 0 &&
			member.Reactivations > 0 && member.InitialAssignments == 1 && member.Observations > 0
	}
}
