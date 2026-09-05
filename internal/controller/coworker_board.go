package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CoworkerBoard struct {
	Revision int64          `json:"revision"`
	Tasks    []CoworkerCard `json:"tasks"`
}
type CoworkerCard struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Status   string            `json:"status"`
	Assignee string            `json:"assignee,omitempty"`
	Comments []CoworkerComment `json:"comments"`
}
type CoworkerComment struct {
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}
type CoworkerBoardEdit struct {
	Revision int64  `json:"revision"`
	Action   string `json:"action"`
	TaskID   string `json:"taskId"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

func applyCoworkerBoardEdit(board *CoworkerBoard, edit CoworkerBoardEdit, author string, now time.Time) error {
	if edit.Revision != board.Revision {
		return fmt.Errorf("board revision conflict; reload before retrying")
	}
	if !loginProfileName.MatchString(edit.TaskID) {
		return fmt.Errorf("task ID must be 1–64 safe name characters")
	}
	index := -1
	for i, task := range board.Tasks {
		if task.ID == edit.TaskID {
			index = i
			break
		}
	}
	switch edit.Action {
	case "create":
		if index >= 0 || len(board.Tasks) >= 100 || strings.TrimSpace(edit.Title) == "" || len(edit.Title) > 512 {
			return fmt.Errorf("new task needs a unique ID and title up to 512 bytes; board limit is 100 tasks")
		}
		board.Tasks = append(board.Tasks, CoworkerCard{ID: edit.TaskID, Title: edit.Title, Status: "todo", Comments: []CoworkerComment{}})
	case "move":
		if index < 0 || (edit.Status != "todo" && edit.Status != "doing" && edit.Status != "done") {
			return fmt.Errorf("existing task and status todo, doing, or done required")
		}
		board.Tasks[index].Status = edit.Status
	case "assign":
		if index < 0 {
			return fmt.Errorf("task not found")
		}
		board.Tasks[index].Assignee = edit.Assignee
	case "comment":
		if index < 0 || strings.TrimSpace(edit.Comment) == "" || len(edit.Comment) > 4096 {
			return fmt.Errorf("existing task and comment up to 4 KiB required")
		}
		if len(board.Tasks[index].Comments) >= 100 {
			return fmt.Errorf("task comment limit reached")
		}
		board.Tasks[index].Comments = append(board.Tasks[index].Comments, CoworkerComment{Author: author, Text: edit.Comment, CreatedAt: now.UTC()})
	default:
		return fmt.Errorf("board action must be create, move, assign, or comment")
	}
	board.Revision++
	return nil
}

func (s *Store) ReadCoworkerBoard(ctx context.Context, id CoworkerIdentity) (CoworkerBoard, error) {
	board := CoworkerBoard{Tasks: []CoworkerCard{}}
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(b.revision,0),COALESCE(b.board,'{"tasks":[]}'::jsonb) FROM coworkers c JOIN coworker_settings g ON g.account_id=c.account_id LEFT JOIN coworker_boards b ON b.account_id=c.account_id WHERE c.account_id=$1 AND c.box_id=$2 AND c.enabled AND g.enabled`, id.AccountID, id.BoxID).Scan(&board.Revision, &raw)
	if err != nil {
		return board, fmt.Errorf("board unavailable for this coworker")
	}
	var content struct {
		Tasks []CoworkerCard `json:"tasks"`
	}
	if err = json.Unmarshal(raw, &content); err != nil {
		return board, err
	}
	board.Tasks = content.Tasks
	return board, nil
}

func (s *Store) EditCoworkerBoard(ctx context.Context, id CoworkerIdentity, edit CoworkerBoardEdit) (CoworkerBoard, error) {
	var board CoworkerBoard
	tx, err := s.beginCoworkerWrite(ctx, id.AccountID)
	if err != nil {
		return board, err
	}
	defer tx.Rollback()
	var enabled bool
	err = tx.QueryRowContext(ctx, `SELECT c.enabled AND g.enabled FROM coworkers c JOIN coworker_settings g ON g.account_id=c.account_id WHERE c.account_id=$1 AND c.box_id=$2 FOR SHARE OF c,g`, id.AccountID, id.BoxID).Scan(&enabled)
	if err != nil || !enabled {
		return board, fmt.Errorf("coworker access disabled")
	}
	if edit.Action == "assign" && edit.Assignee != "" {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coworkers WHERE account_id=$1 AND box_id::text=$2 AND enabled)`, id.AccountID, edit.Assignee).Scan(&exists); err != nil || !exists {
			return board, fmt.Errorf("assignee is not a coworker in this account")
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO coworker_boards(account_id) VALUES($1) ON CONFLICT DO NOTHING`, id.AccountID); err != nil {
		return board, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT revision,board FROM coworker_boards WHERE account_id=$1 FOR UPDATE`, id.AccountID).Scan(&board.Revision, &raw); err != nil {
		return board, err
	}
	var content struct {
		Tasks []CoworkerCard `json:"tasks"`
	}
	if err = json.Unmarshal(raw, &content); err != nil {
		return board, err
	}
	board.Tasks = content.Tasks
	if err = applyCoworkerBoardEdit(&board, edit, id.BoxID, time.Now()); err != nil {
		return board, err
	}
	content.Tasks = board.Tasks
	raw, err = json.Marshal(content)
	if err != nil {
		return board, err
	}
	if len(raw) > 65536 {
		return board, fmt.Errorf("shared board exceeds 64 KiB")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE coworker_boards SET revision=$2,board=$3,updated_at=now() WHERE account_id=$1`, id.AccountID, board.Revision, raw); err != nil {
		return board, err
	}
	event, _ := json.Marshal(map[string]any{"revision": board.Revision, "taskId": edit.TaskID, "action": edit.Action})
	_, err = tx.ExecContext(ctx, `INSERT INTO coworker_events(account_id,recipient_box_id,sender_box_id,message_key,kind,data) SELECT account_id,box_id,$2,'board:'||$3::bigint::text||':'||box_id::text,'board',$4 FROM coworkers WHERE account_id=$1 AND enabled`, id.AccountID, id.BoxID, board.Revision, event)
	if err != nil {
		return board, err
	}
	return board, tx.Commit()
}
