// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store_test

import (
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/testutil"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// commentFixture is a seeded space+page plus the raw DB handle for Posts stand-in rows.
type commentFixture struct {
	store *store.Store
	db    *sql.DB
	space *model.Space
	page  *model.Page
}

func newCommentFixture(t *testing.T) *commentFixture {
	t.Helper()
	s, db := testutil.OpenTestStore(t)
	channelID := mmmodel.NewId()
	space := testutil.MustCreateSpace(t, s, channelID, mmmodel.NewId())
	page := testutil.MustCreatePage(t, s, space.Id, channelID, mmmodel.NewId(), "")
	return &commentFixture{store: s, db: db, space: space, page: page}
}

// seedComment inserts a comment Posts row on the fixture's page. createAt 0 lets the stand-in
// stamp the current millis; mutate applies overrides before insert.
func (f *commentFixture) seedComment(t *testing.T, rootID string, createAt int64, mutate func(*mmmodel.Post)) *mmmodel.Post {
	t.Helper()
	post := &mmmodel.Post{
		Id:        mmmodel.NewId(),
		CreateAt:  createAt,
		UserId:    mmmodel.NewId(),
		ChannelId: f.space.ChannelId,
		RootId:    rootID,
		Message:   "seeded comment",
		Type:      model.PostTypePageComment,
	}
	post.SetProps(mmmodel.StringInterface{model.PropKeyPageId: f.page.Id})
	if mutate != nil {
		mutate(post)
	}
	return testutil.MustInsertPost(t, f.db, post)
}

func listRoots(t *testing.T, f *commentFixture, opts store.PageCommentListOptions) []*mmmodel.Post {
	t.Helper()
	if opts.Limit == 0 {
		opts.Limit = 100
	}
	posts, err := f.store.GetPageCommentRoots(f.space.ChannelId, f.page.Id, opts)
	require.NoError(t, err)
	return posts
}

func postIDs(posts []*mmmodel.Post) []string {
	ids := make([]string, len(posts))
	for i, p := range posts {
		ids[i] = p.Id
	}
	return ids
}

func TestGetPageCommentRoots(t *testing.T) {
	t.Run("returns only this page's live roots in this channel", func(t *testing.T) {
		f := newCommentFixture(t)
		root := f.seedComment(t, "", 1000, nil)
		f.seedComment(t, root.Id, 2000, nil) // reply: excluded
		f.seedComment(t, "", 3000, func(p *mmmodel.Post) { p.DeleteAt = 1 })
		f.seedComment(t, "", 4000, func(p *mmmodel.Post) { // other page, same channel
			p.SetProps(mmmodel.StringInterface{model.PropKeyPageId: mmmodel.NewId()})
		})
		f.seedComment(t, "", 5000, func(p *mmmodel.Post) { p.ChannelId = mmmodel.NewId() }) // same page_id, other channel
		f.seedComment(t, "", 6000, func(p *mmmodel.Post) { p.Type = "" })                   // ordinary chat post

		got := listRoots(t, f, store.PageCommentListOptions{})
		assert.Equal(t, []string{root.Id}, postIDs(got))
	})

	t.Run("orders by CreateAt then Id so ties cannot repeat or drop", func(t *testing.T) {
		f := newCommentFixture(t)
		// Two comments sharing CreateAt: the Id tie-break makes the order total.
		a := f.seedComment(t, "", 1000, func(p *mmmodel.Post) { p.Id = "aaaaaaaaaaaaaaaaaaaaaaaaaa" })
		b := f.seedComment(t, "", 1000, func(p *mmmodel.Post) { p.Id = "bbbbbbbbbbbbbbbbbbbbbbbbbb" })
		c := f.seedComment(t, "", 500, nil)

		got := listRoots(t, f, store.PageCommentListOptions{})
		require.Equal(t, []string{c.Id, a.Id, b.Id}, postIDs(got))

		// Walk with limit 2: the boundary between the tied rows holds.
		first := listRoots(t, f, store.PageCommentListOptions{Limit: 2})
		require.Equal(t, []string{c.Id, a.Id}, postIDs(first))
		second := listRoots(t, f, store.PageCommentListOptions{AfterCreateAt: a.CreateAt, AfterID: a.Id, Limit: 2})
		require.Equal(t, []string{b.Id}, postIDs(second))
	})

	t.Run("a delete before the cursor cannot shift the window", func(t *testing.T) {
		f := newCommentFixture(t)
		var seeded []*mmmodel.Post
		for i := range 6 {
			seeded = append(seeded, f.seedComment(t, "", int64(1000*(i+1)), nil))
		}
		first := listRoots(t, f, store.PageCommentListOptions{Limit: 2})
		require.Equal(t, []string{seeded[0].Id, seeded[1].Id}, postIDs(first))

		// Soft-delete the first comment mid-walk; page 2 is unchanged. OFFSET paging would drop
		// exactly one row here.
		_, err := f.db.Exec(`UPDATE Posts SET DeleteAt = 1 WHERE Id = $1`, seeded[0].Id)
		require.NoError(t, err)

		second := listRoots(t, f, store.PageCommentListOptions{AfterCreateAt: seeded[1].CreateAt, AfterID: seeded[1].Id, Limit: 2})
		assert.Equal(t, []string{seeded[2].Id, seeded[3].Id}, postIDs(second))
	})

	t.Run("a cursor naming a deleted row still resumes", func(t *testing.T) {
		f := newCommentFixture(t)
		var seeded []*mmmodel.Post
		for i := range 4 {
			seeded = append(seeded, f.seedComment(t, "", int64(1000*(i+1)), nil))
		}
		// Delete the row the cursor names; the comparison is a pure value bound, so the walk
		// resumes from the nearest greater row.
		_, err := f.db.Exec(`UPDATE Posts SET DeleteAt = 1 WHERE Id = $1`, seeded[1].Id)
		require.NoError(t, err)

		got := listRoots(t, f, store.PageCommentListOptions{AfterCreateAt: seeded[1].CreateAt, AfterID: seeded[1].Id, Limit: 10})
		assert.Equal(t, []string{seeded[2].Id, seeded[3].Id}, postIDs(got))
	})

	t.Run("resolved filter treats an absent key as unresolved", func(t *testing.T) {
		f := newCommentFixture(t)
		neverResolved := f.seedComment(t, "", 1000, nil)
		resolved := f.seedComment(t, "", 2000, func(p *mmmodel.Post) {
			p.AddProp(model.PropKeyResolved, true)
		})
		explicitlyUnresolved := f.seedComment(t, "", 3000, func(p *mmmodel.Post) {
			p.AddProp(model.PropKeyResolved, false)
		})

		wantTrue := true
		assert.Equal(t, []string{resolved.Id}, postIDs(listRoots(t, f, store.PageCommentListOptions{Resolved: &wantTrue})))

		// A plain != 'true' would match neither row: the key is absent, not 'false', on
		// never-resolved comments.
		wantFalse := false
		assert.Equal(t, []string{neverResolved.Id, explicitlyUnresolved.Id},
			postIDs(listRoots(t, f, store.PageCommentListOptions{Resolved: &wantFalse})))
	})

	t.Run("comment_type filter treats an absent key as footer", func(t *testing.T) {
		f := newCommentFixture(t)
		bare := f.seedComment(t, "", 1000, nil) // no comment_type prop at all
		inline := f.seedComment(t, "", 2000, func(p *mmmodel.Post) {
			p.AddProp(model.PropKeyCommentType, model.CommentTypeInline)
			p.AddProp(model.PropKeyAnchorId, "a1")
		})

		assert.Equal(t, []string{bare.Id}, postIDs(listRoots(t, f, store.PageCommentListOptions{CommentType: model.CommentTypeFooter})))
		assert.Equal(t, []string{inline.Id}, postIDs(listRoots(t, f, store.PageCommentListOptions{CommentType: model.CommentTypeInline})))
	})

	t.Run("filters compose", func(t *testing.T) {
		f := newCommentFixture(t)
		f.seedComment(t, "", 1000, func(p *mmmodel.Post) {
			p.AddProp(model.PropKeyCommentType, model.CommentTypeInline)
			p.AddProp(model.PropKeyAnchorId, "a1")
			p.AddProp(model.PropKeyResolved, true)
		})
		footerResolved := f.seedComment(t, "", 2000, func(p *mmmodel.Post) {
			p.AddProp(model.PropKeyResolved, true)
		})

		wantTrue := true
		got := listRoots(t, f, store.PageCommentListOptions{Resolved: &wantTrue, CommentType: model.CommentTypeFooter})
		assert.Equal(t, []string{footerResolved.Id}, postIDs(got))
	})

	t.Run("a non-positive limit is invalid input", func(t *testing.T) {
		f := newCommentFixture(t)
		_, err := f.store.GetPageCommentRoots(f.space.ChannelId, f.page.Id, store.PageCommentListOptions{})
		assert.True(t, store.IsErrInvalidInput(err))
	})
}

func TestGetPageCommentReplies(t *testing.T) {
	f := newCommentFixture(t)
	root := f.seedComment(t, "", 1000, nil)
	r1 := f.seedComment(t, root.Id, 2000, nil)
	r2 := f.seedComment(t, root.Id, 3000, nil)
	f.seedComment(t, root.Id, 4000, func(p *mmmodel.Post) { p.DeleteAt = 1 })
	otherRoot := f.seedComment(t, "", 1500, nil)
	f.seedComment(t, otherRoot.Id, 2500, nil)

	got, err := f.store.GetPageCommentReplies(f.space.ChannelId, f.page.Id, root.Id, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{r1.Id, r2.Id}, postIDs(got))

	offsetPage, err := f.store.GetPageCommentReplies(f.space.ChannelId, f.page.Id, root.Id, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{r2.Id}, postIDs(offsetPage))
}

func TestGetPageComment(t *testing.T) {
	f := newCommentFixture(t)
	root := f.seedComment(t, "", 1000, nil)
	deleted := f.seedComment(t, "", 2000, func(p *mmmodel.Post) { p.DeleteAt = 5 })
	chatPost := f.seedComment(t, "", 3000, func(p *mmmodel.Post) { p.Type = "" })

	t.Run("resolves through the full identity predicate", func(t *testing.T) {
		got, err := f.store.GetPageComment(root.Id, f.space.ChannelId, f.page.Id, false)
		require.NoError(t, err)
		assert.Equal(t, root.Id, got.Id)
		assert.Equal(t, f.page.Id, got.GetProp(model.PropKeyPageId))
	})

	t.Run("wrong page, wrong channel, and an ordinary post all read as not-found", func(t *testing.T) {
		_, err := f.store.GetPageComment(root.Id, f.space.ChannelId, mmmodel.NewId(), false)
		assert.True(t, store.IsErrNotFound(err))

		_, err = f.store.GetPageComment(root.Id, mmmodel.NewId(), f.page.Id, false)
		assert.True(t, store.IsErrNotFound(err))

		_, err = f.store.GetPageComment(chatPost.Id, f.space.ChannelId, f.page.Id, false)
		assert.True(t, store.IsErrNotFound(err))
	})

	t.Run("includeDeleted is only for the committed-state probe", func(t *testing.T) {
		_, err := f.store.GetPageComment(deleted.Id, f.space.ChannelId, f.page.Id, false)
		assert.True(t, store.IsErrNotFound(err))

		got, err := f.store.GetPageComment(deleted.Id, f.space.ChannelId, f.page.Id, true)
		require.NoError(t, err)
		assert.EqualValues(t, 5, got.DeleteAt)
	})
}

func TestGetPageCommentReplyCounts(t *testing.T) {
	f := newCommentFixture(t)
	rootA := f.seedComment(t, "", 1000, nil)
	rootB := f.seedComment(t, "", 2000, nil)
	rootC := f.seedComment(t, "", 3000, nil)
	f.seedComment(t, rootA.Id, 4000, nil)
	f.seedComment(t, rootA.Id, 5000, nil)
	f.seedComment(t, rootB.Id, 6000, nil)
	f.seedComment(t, rootB.Id, 7000, func(p *mmmodel.Post) { p.DeleteAt = 1 })

	counts, err := f.store.GetPageCommentReplyCounts([]string{rootA.Id, rootB.Id, rootC.Id})
	require.NoError(t, err)
	assert.Equal(t, 2, counts[rootA.Id])
	assert.Equal(t, 1, counts[rootB.Id])
	// A root with no live replies is absent from the map, reading as zero.
	assert.NotContains(t, counts, rootC.Id)

	empty, err := f.store.GetPageCommentReplyCounts(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestGetPageCommentCounts(t *testing.T) {
	resolved := func(p *mmmodel.Post) {
		p.SetProps(mmmodel.StringInterface{
			model.PropKeyPageId:   p.GetProps()[model.PropKeyPageId],
			model.PropKeyResolved: true,
		})
	}

	t.Run("a page with no comments counts zero", func(t *testing.T) {
		f := newCommentFixture(t)
		counts, err := f.store.GetPageCommentCounts(f.space.ChannelId, f.page.Id)
		require.NoError(t, err)
		assert.Equal(t, store.PageCommentCounts{}, counts)
	})

	t.Run("roots split by resolve state; replies, deleted rows and history never count", func(t *testing.T) {
		f := newCommentFixture(t)
		rootA := f.seedComment(t, "", 1000, nil)
		f.seedComment(t, "", 2000, nil)
		f.seedComment(t, "", 3000, resolved)
		f.seedComment(t, rootA.Id, 4000, nil)                                // reply
		f.seedComment(t, "", 5000, func(p *mmmodel.Post) { p.DeleteAt = 1 }) // soft-deleted root
		f.seedComment(t, "", 5500, func(p *mmmodel.Post) {                   // a root's edit-history row
			p.OriginalId = rootA.Id
			p.DeleteAt = 1
		})

		counts, err := f.store.GetPageCommentCounts(f.space.ChannelId, f.page.Id)
		require.NoError(t, err)
		assert.Equal(t, store.PageCommentCounts{Total: 3, Open: 2, Resolved: 1}, counts)
		assert.Equal(t, counts.Total, counts.Open+counts.Resolved, "the split must reconcile")
	})

	t.Run("resolved is read as a string 'true' too", func(t *testing.T) {
		// Props round-trip through JSON, so the flag can come back either encoding.
		f := newCommentFixture(t)
		f.seedComment(t, "", 1000, func(p *mmmodel.Post) {
			p.SetProps(mmmodel.StringInterface{
				model.PropKeyPageId:   p.GetProps()[model.PropKeyPageId],
				model.PropKeyResolved: "true",
			})
		})

		counts, err := f.store.GetPageCommentCounts(f.space.ChannelId, f.page.Id)
		require.NoError(t, err)
		assert.Equal(t, store.PageCommentCounts{Total: 1, Open: 0, Resolved: 1}, counts)
	})

	t.Run("another page and another channel are out of scope", func(t *testing.T) {
		f := newCommentFixture(t)
		f.seedComment(t, "", 1000, nil)
		f.seedComment(t, "", 2000, func(p *mmmodel.Post) {
			p.SetProps(mmmodel.StringInterface{model.PropKeyPageId: mmmodel.NewId()})
		})
		f.seedComment(t, "", 3000, func(p *mmmodel.Post) { p.ChannelId = mmmodel.NewId() })

		counts, err := f.store.GetPageCommentCounts(f.space.ChannelId, f.page.Id)
		require.NoError(t, err)
		assert.Equal(t, store.PageCommentCounts{Total: 1, Open: 1}, counts)
	})
}

func TestGetMisplacedCommentRoots(t *testing.T) {
	f := newCommentFixture(t)
	// A stranded comment sits on the backing channel of the space its page was moved out of, so
	// the source is always a real space channel — which is what makes the core move accept it.
	strandedChannelID := mmmodel.NewId()
	testutil.MustCreateSpace(t, f.store, strandedChannelID, mmmodel.NewId())

	misplacedA := f.seedComment(t, "", 1000, func(p *mmmodel.Post) { p.ChannelId = strandedChannelID })
	misplacedB := f.seedComment(t, "", 2000, func(p *mmmodel.Post) { p.ChannelId = strandedChannelID })
	f.seedComment(t, "", 3000, nil)                                                                  // correct channel: not misplaced
	f.seedComment(t, misplacedA.Id, 4000, func(p *mmmodel.Post) { p.ChannelId = strandedChannelID }) // reply: the move is keyed on roots
	deletedMisplaced := f.seedComment(t, "", 5000, func(p *mmmodel.Post) {
		p.ChannelId = strandedChannelID
		p.DeleteAt = 1
	})
	f.seedComment(t, "", 5500, func(p *mmmodel.Post) { // a root's edit-history row: RootId='' but OriginalId set
		p.ChannelId = strandedChannelID
		p.OriginalId = misplacedA.Id
		p.DeleteAt = 1
	})
	f.seedComment(t, "", 6000, func(p *mmmodel.Post) { // another space's page: out of scope
		p.ChannelId = strandedChannelID
		p.SetProps(mmmodel.StringInterface{model.PropKeyPageId: mmmodel.NewId()})
	})

	t.Run("returns stranded roots only, deleted ones included, history rows never, ordered by id", func(t *testing.T) {
		got, err := f.store.GetMisplacedCommentRoots(f.space.Id, f.space.ChannelId, nil, 10)
		require.NoError(t, err)
		want := []string{misplacedA.Id, misplacedB.Id, deletedMisplaced.Id}
		slices.Sort(want)
		assert.Equal(t, want, got)
	})

	t.Run("a source-channel narrowing sees only rows on those channels", func(t *testing.T) {
		got, err := f.store.GetMisplacedCommentRoots(f.space.Id, f.space.ChannelId, []string{strandedChannelID}, 10)
		require.NoError(t, err)
		assert.Len(t, got, 3)

		got, err = f.store.GetMisplacedCommentRoots(f.space.Id, f.space.ChannelId, []string{mmmodel.NewId()}, 10)
		require.NoError(t, err)
		assert.Empty(t, got, "stragglers on other channels are the repair path's job")
	})

	t.Run("a comment-shaped row on an ordinary channel is never named", func(t *testing.T) {
		// Core rejects a whole move batch containing a channel that is not a space, so a row
		// anyone can post — the type and the page_id prop are both writable through the ordinary
		// post API — would otherwise fail every sweep and strand the real ones for good.
		f.seedComment(t, "", 7000, func(p *mmmodel.Post) { p.ChannelId = mmmodel.NewId() })

		got, err := f.store.GetMisplacedCommentRoots(f.space.Id, f.space.ChannelId, nil, 10)
		require.NoError(t, err)
		want := []string{misplacedA.Id, misplacedB.Id, deletedMisplaced.Id}
		slices.Sort(want)
		assert.Equal(t, want, got)
	})

	t.Run("the limit bounds one chunk", func(t *testing.T) {
		got, err := f.store.GetMisplacedCommentRoots(f.space.Id, f.space.ChannelId, nil, 2)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("a repaired space reports nothing", func(t *testing.T) {
		_, err := f.db.Exec(`UPDATE Posts SET ChannelId = $1 WHERE ChannelId = $2`, f.space.ChannelId, strandedChannelID)
		require.NoError(t, err)
		got, err := f.store.GetMisplacedCommentRoots(f.space.Id, f.space.ChannelId, nil, 10)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestWithPageCommentLock(t *testing.T) {
	t.Run("hands the callback the locked page identity", func(t *testing.T) {
		f := newCommentFixture(t)
		called := false
		err := f.store.WithPageCommentLock(f.page.Id, "", func(tx *sqlx.Tx, locked store.LockedPage) error {
			called = true
			assert.Equal(t, f.space.Id, locked.SpaceId)
			assert.Equal(t, f.space.ChannelId, locked.ChannelId)
			// In-lock reads ride the lock's transaction.
			_, err := f.store.GetPageCommentReplyCountsTx(tx, []string{mmmodel.NewId()})
			return err
		})
		require.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("a missing or deleted page is not lockable", func(t *testing.T) {
		f := newCommentFixture(t)
		err := f.store.WithPageCommentLock(mmmodel.NewId(), "", func(*sqlx.Tx, store.LockedPage) error {
			t.Fatal("callback must not run for a missing page")
			return nil
		})
		assert.True(t, store.IsErrNotFound(err))

		_, delErr := f.store.DeletePage(f.page.Id, f.space.Id, mmmodel.NewId())
		require.NoError(t, delErr)
		err = f.store.WithPageCommentLock(f.page.Id, "", func(*sqlx.Tx, store.LockedPage) error {
			t.Fatal("callback must not run for a deleted page")
			return nil
		})
		assert.True(t, store.IsErrNotFound(err))
	})

	t.Run("a callback error propagates unchanged", func(t *testing.T) {
		f := newCommentFixture(t)
		sentinel := &store.ErrInvalidInput{Entity: "test", Field: "field", Value: 1}
		err := f.store.WithPageCommentLock(f.page.Id, mmmodel.NewId(), func(*sqlx.Tx, store.LockedPage) error {
			return sentinel
		})
		assert.ErrorIs(t, err, sentinel)
	})

	t.Run("a concurrent move blocks until the lock is released", func(t *testing.T) {
		f := newCommentFixture(t)
		targetSpace := testutil.MustCreateSpace(t, f.store, mmmodel.NewId(), f.space.TeamId)

		release := make(chan struct{})
		lockHeld := make(chan struct{})
		lockDone := make(chan error, 1)
		go func() {
			lockDone <- f.store.WithPageCommentLock(f.page.Id, "", func(*sqlx.Tx, store.LockedPage) error {
				close(lockHeld)
				<-release
				return nil
			})
		}()
		<-lockHeld

		moveDone := make(chan error, 1)
		go func() {
			_, _, err := f.store.MovePageToSpace(f.page.Id, f.space.Id, targetSpace.Id, mmmodel.NewId(), nil, 0, true, model.MaxPageDepth)
			moveDone <- err
		}()

		// The move must be waiting on the page row lock, not committed.
		select {
		case err := <-moveDone:
			t.Fatalf("move committed while the comment lock was held (err=%v)", err)
		case <-time.After(300 * time.Millisecond):
		}

		close(release)
		require.NoError(t, <-lockDone)
		require.NoError(t, <-moveDone)

		// The move observed the released lock and completed: the page is in the target space.
		moved, err := f.store.GetPage(f.page.Id, false)
		require.NoError(t, err)
		assert.Equal(t, targetSpace.Id, moved.SpaceId)
	})
}

// TestWithPageCommentLockThreadSerialization pins the property the threadLockKey rule exists for:
// two writes anywhere in one comment thread must not run at once. The page row lock alone does not
// give this — it is taken FOR SHARE, so two comment writes on the same page hold it concurrently.
func TestWithPageCommentLockThreadSerialization(t *testing.T) {
	t.Run("the same thread key serializes two writes on one page", func(t *testing.T) {
		f := newCommentFixture(t)
		rootID := mmmodel.NewId()

		release := make(chan struct{})
		firstHeld := make(chan struct{})
		firstDone := make(chan error, 1)
		go func() {
			firstDone <- f.store.WithPageCommentLock(f.page.Id, rootID, func(*sqlx.Tx, store.LockedPage) error {
				close(firstHeld)
				<-release
				return nil
			})
		}()
		<-firstHeld

		secondEntered := make(chan struct{})
		secondDone := make(chan error, 1)
		go func() {
			secondDone <- f.store.WithPageCommentLock(f.page.Id, rootID, func(*sqlx.Tx, store.LockedPage) error {
				close(secondEntered)
				return nil
			})
		}()

		select {
		case <-secondEntered:
			t.Fatal("a second write on the same thread entered while the first held the thread lock")
		case <-time.After(300 * time.Millisecond):
		}

		close(release)
		require.NoError(t, <-firstDone)
		require.NoError(t, <-secondDone)
		<-secondEntered
	})

	t.Run("a different thread key on the same page does not block", func(t *testing.T) {
		f := newCommentFixture(t)

		release := make(chan struct{})
		firstHeld := make(chan struct{})
		firstDone := make(chan error, 1)
		go func() {
			firstDone <- f.store.WithPageCommentLock(f.page.Id, mmmodel.NewId(), func(*sqlx.Tx, store.LockedPage) error {
				close(firstHeld)
				<-release
				return nil
			})
		}()
		<-firstHeld

		// A write on another thread of the same page must proceed: the page row lock is FOR
		// SHARE, so only the thread key can be what serializes.
		require.NoError(t, f.store.WithPageCommentLock(f.page.Id, mmmodel.NewId(), func(*sqlx.Tx, store.LockedPage) error {
			return nil
		}))

		close(release)
		require.NoError(t, <-firstDone)
	})
}
