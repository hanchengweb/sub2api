package collaboration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

// Write commits the effect and replay record together. Membership is rechecked
// before returning a cached room result, so idempotency cannot bypass revocation.
func (s *Store) Write(ctx context.Context, key *service.APIKey, op, room, id string, in Input) (any, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if !validID(in.ClientRequestID) {
		return nil, invalid("clientRequestId 必须为 UUID")
	}
	raw, _ := json.Marshal([]any{op, room, id, in})
	fingerprint := digest(raw)
	return s.transaction(ctx, func(tx *sql.Tx) (any, error) {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1464353875,hashint8($1::bigint))`, key.UserID); err != nil {
			return nil, err
		}
		if room != "" {
			if _, err := roomLock(ctx, tx, room, key.UserID, false); err != nil {
				return nil, err
			}
		}
		var previous string
		var cached []byte
		err := tx.QueryRowContext(ctx, `SELECT fingerprint,result FROM desktop_collaboration_requests WHERE user_id=$1 AND request_id=$2`, key.UserID, in.ClientRequestID).Scan(&previous, &cached)
		if err == nil {
			if previous != fingerprint {
				return nil, conflict
			}
			var result any
			err = json.Unmarshal(cached, &result)
			// Join/create replays also require current access.
			if op == "join" || op == "create" {
				if obj, ok := result.(map[string]any); ok {
					if rid, ok := obj["id"].(string); ok {
						if _, e := roomLock(ctx, tx, rid, key.UserID, false); e != nil {
							return nil, e
						}
					}
				}
			}
			return result, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		result, err := s.write(ctx, tx, key, op, room, id, in)
		if err != nil {
			return nil, err
		}
		persisted := result
		if op == "invite" {
			// The bearer secret is displayed exactly once. Never store it in
			// the idempotency cache, logs, or any other persistent plaintext.
			copy := map[string]any{}
			for k, v := range result.(map[string]any) {
				if k != "token" {
					copy[k] = v
				}
			}
			copy["token"] = ""
			copy["tokenAlreadyIssued"] = true
			persisted = copy
		}
		data, err := json.Marshal(persisted)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_requests(user_id,request_id,fingerprint,result) VALUES($1,$2,$3,$4)`, key.UserID, in.ClientRequestID, fingerprint, string(data))
		return result, err
	})
}

func (s *Store) write(ctx context.Context, tx *sql.Tx, key *service.APIKey, op, room, id string, in Input) (any, error) {
	uid := key.UserID
	switch op {
	case "create":
		name := strings.TrimSpace(in.Name)
		project := strings.TrimSpace(in.ProjectName)
		if !validateText(name, 160) || project != "" && !validateText(project, 160) {
			return nil, invalid("群名或项目名无效")
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM desktop_collaboration_rooms WHERE owner_id=$1`, uid).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 100 {
			return nil, invalid("已达到个人群聊上限")
		}
		rid := uuid.NewString()
		var projectID any
		if project != "" {
			projectID = uuid.NewString()
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_rooms(id,owner_id,name,project_id,project_name) VALUES($1,$2,$3,$4,$5)`, rid, uid, name, projectID, project)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_members(room_id,user_id) VALUES($1,$2)`, rid, uid)
		if err != nil {
			return nil, err
		}
		return roomLock(ctx, tx, rid, uid, false)
	case "invite":
		if _, err := roomLock(ctx, tx, room, uid, true); err != nil {
			return nil, err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM desktop_collaboration_invites WHERE room_id=$1 AND NOT revoked AND expires_at>now()`, room).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 20 {
			return nil, invalid("请先撤销旧邀请")
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		token := base64.RawURLEncoding.EncodeToString(secret)
		inviteID := uuid.NewString()
		expires := time.Now().UTC().Add(24 * time.Hour)
		_, err := tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_invites(id,room_id,token_hash,expires_at) VALUES($1,$2,$3,$4)`, inviteID, room, digest([]byte(token)), expires)
		return map[string]any{"id": inviteID, "token": token, "expiresAt": expires}, err
	case "revokeInvite":
		if _, err := roomLock(ctx, tx, room, uid, true); err != nil {
			return nil, err
		}
		if !validID(id) {
			return nil, invalid("邀请无效")
		}
		_, err := tx.ExecContext(ctx, `UPDATE desktop_collaboration_invites SET revoked=true WHERE room_id=$1 AND id=$2`, room, id)
		return map[string]any{}, err
	case "join":
		if len(in.Token) != 43 {
			return nil, invalid("邀请无效或已过期")
		}
		var rid string
		if err := tx.QueryRowContext(ctx, `SELECT room_id::text FROM desktop_collaboration_invites WHERE token_hash=$1 AND NOT revoked AND expires_at>now()`, digest([]byte(in.Token))).Scan(&rid); errors.Is(err, sql.ErrNoRows) {
			return nil, invalid("邀请无效或已过期")
		} else if err != nil {
			return nil, err
		}
		// Same lock order as all room mutations; recheck invite under the lock.
		if _, err := tx.ExecContext(ctx, `SELECT id FROM desktop_collaboration_rooms WHERE id=$1 FOR UPDATE`, rid); err != nil {
			return nil, err
		}
		var allowed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM desktop_collaboration_invites WHERE room_id=$1 AND token_hash=$2 AND NOT revoked AND expires_at>now())`, rid, digest([]byte(in.Token))).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			return nil, invalid("邀请无效或已过期")
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM desktop_collaboration_members WHERE room_id=$1 AND active`, rid).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 100 {
			return nil, invalid("群聊成员已满")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_members(room_id,user_id) VALUES($1,$2) ON CONFLICT(room_id,user_id) DO UPDATE SET active=true,joined_at=now()`, rid, uid)
		if err != nil {
			return nil, err
		}
		return roomLock(ctx, tx, rid, uid, false)
	case "removeMember":
		r, err := roomLock(ctx, tx, room, uid, true)
		if err != nil {
			return nil, err
		}
		member, err := userID(id)
		if err != nil {
			return nil, err
		}
		if id == r.OwnerID {
			return nil, invalid("不能移除群主")
		}
		_, err = tx.ExecContext(ctx, `UPDATE desktop_collaboration_members SET active=false WHERE room_id=$1 AND user_id=$2`, room, member)
		if err != nil {
			return nil, err
		}
		// A removed member cannot use an outstanding invitation to silently
		// re-enter; the owner must issue a fresh explicit invitation.
		_, err = tx.ExecContext(ctx, `UPDATE desktop_collaboration_invites SET revoked=true WHERE room_id=$1`, room)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE desktop_collaboration_tasks SET assignee_id=NULL,revision=revision+1 WHERE room_id=$1 AND assignee_id=$2`, room, member)
		return map[string]any{}, err
	case "addAgent":
		if _, err := roomLock(ctx, tx, room, uid, true); err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Name)
		model := strings.TrimSpace(in.Model)
		if !validateText(name, 160) || !validateText(in.Instructions, 8000) || !validateText(model, 200) || !textModel(model) {
			return nil, invalid("智能体名称、指令或模型无效")
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM desktop_collaboration_agents WHERE room_id=$1 AND active`, room).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 12 {
			return nil, invalid("每群最多添加12个智能体")
		}
		a := Agent{uuid.NewString(), name, in.Instructions, model}
		_, err := tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_agents(id,room_id,name,instructions,model) VALUES($1,$2,$3,$4,$5)`, a.ID, room, a.Name, a.Instructions, a.Model)
		return a, err
	case "removeAgent":
		if _, err := roomLock(ctx, tx, room, uid, true); err != nil {
			return nil, err
		}
		if !validID(id) {
			return nil, invalid("智能体无效")
		}
		_, err := tx.ExecContext(ctx, `UPDATE desktop_collaboration_agents SET active=false WHERE room_id=$1 AND id=$2`, room, id)
		return map[string]any{}, err
	case "message":
		if (!validateText(in.Text, 12000) && !(in.Text == "" && len(in.AttachmentIDs) > 0)) || len(in.MentionIDs) > 6 || len(in.AttachmentIDs) > 8 {
			return nil, invalid("消息为空、过长或提及过多")
		}
		mentions := append([]string(nil), in.MentionIDs...)
		sort.Strings(mentions)
		agents := []Agent{}
		for i, aid := range mentions {
			if !validID(aid) {
				return nil, invalid("提及的智能体无效")
			}
			if i > 0 && mentions[i-1] == aid {
				continue
			}
			var a Agent
			err := tx.QueryRowContext(ctx, `SELECT id::text,name,instructions,model FROM desktop_collaboration_agents WHERE room_id=$1 AND id=$2 AND active`, room, aid).Scan(&a.ID, &a.Name, &a.Instructions, &a.Model)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, invalid("提及的智能体已移除")
			}
			if err != nil {
				return nil, err
			}
			agents = append(agents, a)
		}
		var count int
		if len(agents) > 0 {
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM desktop_collaboration_runs WHERE payer_id=$1 AND status IN ('queued','running')`, uid).Scan(&count); err != nil {
				return nil, err
			}
			if count+len(agents) > 12 {
				return nil, invalid("待回复问题较多，请稍后再问")
			}
		}
		m, err := insertMessage(ctx, tx, room, Message{ID: uuid.NewString(), Kind: "human", AuthorID: strconv.FormatInt(uid, 10), AuthorName: displayName(key), Text: in.Text})
		if err != nil {
			return nil, err
		}
		seenAssets := map[string]bool{}
		for _, assetID := range in.AttachmentIDs {
			if !validID(assetID) {
				return nil, invalid("附件无效")
			}
			if seenAssets[assetID] {
				continue
			}
			seenAssets[assetID] = true
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM desktop_collaboration_assets WHERE room_id=$1 AND id=$2)`, room, assetID).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, denied
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_message_assets(room_id,message_id,asset_id) VALUES($1,$2,$3)`, room, m.ID, assetID); err != nil {
				return nil, err
			}
		}
		m.Attachments, _, err = modelAttachments(ctx, tx, room, m.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range m.Attachments {
			if _, err = tx.ExecContext(ctx, `UPDATE desktop_collaboration_message_assets SET context_note=$3 WHERE message_id=$1 AND asset_id=$2`, m.ID, a.ID, a.ContextNote); err != nil {
				return nil, err
			}
		}
		runs := []Run{}
		for _, a := range agents {
			run := Run{ID: uuid.NewString(), MessageID: m.ID, AgentID: a.ID, AgentName: a.Name, Status: "queued"}
			_, err = tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_runs(id,room_id,message_id,agent_id,agent_name,instructions,model,payer_id,key_id,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued')`, run.ID, room, m.ID, a.ID, a.Name, a.Instructions, a.Model, uid, key.ID)
			if err != nil {
				return nil, err
			}
			runs = append(runs, run)
		}
		return map[string]any{"message": m, "runs": runs}, nil
	case "task", "updateTask":
		r, err := roomLock(ctx, tx, room, uid, false)
		if err != nil {
			return nil, err
		}
		if r.ProjectID == "" {
			return nil, invalid("此群未关联共享项目")
		}
		var task Task
		if op == "updateTask" {
			if !validID(id) || in.Revision < 1 {
				return nil, invalid("任务与 revision 无效")
			}
			err = tx.QueryRowContext(ctx, `SELECT id::text,title,status,COALESCE(assignee_id::text,''),created_at,revision FROM desktop_collaboration_tasks WHERE room_id=$1 AND id=$2`, room, id).Scan(&task.ID, &task.Title, &task.Status, &task.AssigneeID, &task.CreatedAt, &task.Revision)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, denied
			}
			if err != nil {
				return nil, err
			}
			if task.Revision != in.Revision {
				return nil, conflict
			}
		}
		if op == "task" {
			task.ID = uuid.NewString()
			task.Status = "todo"
			task.Revision = 1
		}
		if in.Title != nil {
			task.Title = strings.TrimSpace(*in.Title)
		}
		if in.Status != nil {
			task.Status = *in.Status
		}
		if in.AssigneeID != nil {
			task.AssigneeID = *in.AssigneeID
		}
		if !validateText(task.Title, 240) || (task.Status != "todo" && task.Status != "doing" && task.Status != "done") {
			return nil, invalid("任务标题或状态无效")
		}
		var assignee any
		if task.AssigneeID != "" {
			member, err := userID(task.AssigneeID)
			if err != nil {
				return nil, err
			}
			var active bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM desktop_collaboration_members WHERE room_id=$1 AND user_id=$2 AND active)`, room, member).Scan(&active); err != nil {
				return nil, err
			}
			if !active {
				return nil, invalid("任务负责人已不在群内")
			}
			assignee = member
		}
		if op == "task" {
			err = tx.QueryRowContext(ctx, `INSERT INTO desktop_collaboration_tasks(id,room_id,title,status,assignee_id) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, task.ID, room, task.Title, task.Status, assignee).Scan(&task.CreatedAt)
		} else {
			err = tx.QueryRowContext(ctx, `UPDATE desktop_collaboration_tasks SET title=$3,status=$4,assignee_id=$5,revision=revision+1 WHERE room_id=$1 AND id=$2 AND revision=$6 RETURNING revision`, room, id, task.Title, task.Status, assignee, in.Revision).Scan(&task.Revision)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, conflict
			}
		}
		return task, err
	}
	return nil, invalid("操作无效")
}

func insertMessage(ctx context.Context, tx *sql.Tx, room string, m Message) (Message, error) {
	err := tx.QueryRowContext(ctx, `UPDATE desktop_collaboration_rooms SET next_seq=next_seq+1 WHERE id=$1 RETURNING next_seq`, room).Scan(&m.Seq)
	if err != nil {
		return m, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO desktop_collaboration_messages(id,room_id,seq,kind,author_id,author_name,text,reply_to_id,run_id) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid)`, m.ID, room, m.Seq, m.Kind, m.AuthorID, m.AuthorName, m.Text, m.ReplyToID, m.RunID)
	if err != nil {
		return m, err
	}
	err = tx.QueryRowContext(ctx, `SELECT created_at FROM desktop_collaboration_messages WHERE id=$1`, m.ID).Scan(&m.CreatedAt)
	return m, err
}

func (s *Store) Read(ctx context.Context, uid int64, op, room string, after int64, limit int) (any, error) {
	return s.transaction(ctx, func(tx *sql.Tx) (any, error) {
		if op == "rooms" {
			rows, err := tx.QueryContext(ctx, `SELECT r.id::text,r.name,r.owner_id::text,COALESCE(r.project_id::text,''),CASE WHEN r.owner_id=$1 THEN 'owner' ELSE 'member' END,(SELECT count(*) FROM desktop_collaboration_members WHERE room_id=r.id AND active) FROM desktop_collaboration_rooms r JOIN desktop_collaboration_members m ON m.room_id=r.id AND m.user_id=$1 AND m.active ORDER BY r.created_at DESC LIMIT 500`, uid)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			items := []Room{}
			for rows.Next() {
				var r Room
				if err = rows.Scan(&r.ID, &r.Name, &r.OwnerID, &r.ProjectID, &r.Role, &r.MemberCount); err != nil {
					return nil, err
				}
				items = append(items, r)
			}
			return map[string]any{"items": items}, rows.Err()
		}
		r, err := roomLock(ctx, tx, room, uid, false)
		if err != nil {
			return nil, err
		}
		switch op {
		case "room":
			members := []Member{}
			rows, err := tx.QueryContext(ctx, `SELECT m.user_id::text,COALESCE(NULLIF(u.username,''),'用户 '||u.id::text),CASE WHEN m.user_id=$2 THEN 'owner' ELSE 'member' END FROM desktop_collaboration_members m JOIN users u ON u.id=m.user_id WHERE m.room_id=$1 AND m.active ORDER BY m.joined_at,m.user_id`, room, r.OwnerID)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var m Member
				if err = rows.Scan(&m.UserID, &m.Name, &m.Role); err != nil {
					rows.Close()
					return nil, err
				}
				m.Name = truncate(m.Name, 160)
				members = append(members, m)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			agents := []Agent{}
			rows, err = tx.QueryContext(ctx, `SELECT id::text,name,instructions,model FROM desktop_collaboration_agents WHERE room_id=$1 AND active ORDER BY created_at,id`, room)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var a Agent
				if err = rows.Scan(&a.ID, &a.Name, &a.Instructions, &a.Model); err != nil {
					rows.Close()
					return nil, err
				}
				agents = append(agents, a)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			result := map[string]any{"room": r, "members": members, "agents": agents}
			if r.ProjectID != "" {
				var name string
				if err = tx.QueryRowContext(ctx, `SELECT project_name FROM desktop_collaboration_rooms WHERE id=$1`, room).Scan(&name); err != nil {
					return nil, err
				}
				result["project"] = map[string]any{"id": r.ProjectID, "name": name}
			}
			return result, nil
		case "messages":
			if after < 0 || limit < 1 || limit > 100 {
				return nil, invalid("消息游标无效")
			}
			rows, err := tx.QueryContext(ctx, `SELECT id::text,seq,kind,author_id,author_name,text,created_at,COALESCE(reply_to_id::text,''),COALESCE(run_id::text,'') FROM desktop_collaboration_messages WHERE room_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, room, after, limit+1)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			items := []Message{}
			for rows.Next() {
				var m Message
				if err = rows.Scan(&m.ID, &m.Seq, &m.Kind, &m.AuthorID, &m.AuthorName, &m.Text, &m.CreatedAt, &m.ReplyToID, &m.RunID); err != nil {
					return nil, err
				}
				items = append(items, m)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			more := len(items) > limit
			if more {
				items = items[:limit]
			}
			// Bound the encoded payload, including HTML escapes and attachment
			// metadata. Desktop clients have a 2MiB JSON response limit.
			page := []Message{}
			encodedBytes := 2
			for i := range items {
				items[i].Attachments, err = assetRows(ctx, tx, room, items[i].ID)
				if err != nil {
					return nil, err
				}
				encoded, encodeErr := json.Marshal(items[i])
				if encodeErr != nil {
					return nil, encodeErr
				}
				if len(page) > 0 && encodedBytes+len(encoded)+1 > 1<<20 {
					more = true
					break
				}
				page = append(page, items[i])
				encodedBytes += len(encoded) + 1
			}
			cursor := after
			if len(page) > 0 {
				cursor = page[len(page)-1].Seq
			}
			return map[string]any{"items": page, "hasMore": more, "cursor": cursor}, nil
		case "runs":
			rows, err := tx.QueryContext(ctx, `SELECT id::text,message_id::text,agent_id::text,agent_name,status,error_code,error_message FROM desktop_collaboration_runs WHERE room_id=$1 ORDER BY created_at DESC LIMIT 100`, room)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			items := []Run{}
			for rows.Next() {
				var run Run
				if err = rows.Scan(&run.ID, &run.MessageID, &run.AgentID, &run.AgentName, &run.Status, &run.ErrorCode, &run.ErrorMessage); err != nil {
					return nil, err
				}
				items = append(items, run)
			}
			return map[string]any{"items": items}, rows.Err()
		case "tasks":
			rows, err := tx.QueryContext(ctx, `SELECT id::text,title,status,COALESCE(assignee_id::text,''),created_at,revision FROM desktop_collaboration_tasks WHERE room_id=$1 ORDER BY created_at,id LIMIT 1000`, room)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			items := []Task{}
			for rows.Next() {
				var t Task
				if err = rows.Scan(&t.ID, &t.Title, &t.Status, &t.AssigneeID, &t.CreatedAt, &t.Revision); err != nil {
					return nil, err
				}
				items = append(items, t)
			}
			return map[string]any{"items": items}, rows.Err()
		}
		return nil, fmt.Errorf("unsupported collaboration read")
	})
}
