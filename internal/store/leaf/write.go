package leaf

import (
	"context"
	"fmt"
	"strings"

	"github.com/gopasspw/gopass/internal/debug"
	"github.com/gopasspw/gopass/internal/out"
	"github.com/gopasspw/gopass/internal/store"
	"github.com/gopasspw/gopass/pkg/ctxutil"
	"github.com/gopasspw/gopass/pkg/gopass"

	"github.com/pkg/errors"
)

// Set encodes and writes the cipertext of one entry to disk
func (s *Store) Set(ctx context.Context, name string, sec gopass.Byter) error {
	if strings.Contains(name, "//") {
		return errors.Errorf("invalid secret name: %s", name)
	}

	p := s.passfile(name)

	recipients, err := s.useableKeys(ctx, name)
	if err != nil {
		return errors.Wrapf(err, "failed to list useable keys for '%s'", p)
	}

	// confirm recipients
	newRecipients, err := GetRecipientFunc(ctx)(ctx, name, recipients)
	if err != nil {
		return errors.Wrapf(err, "user aborted")
	}
	recipients = newRecipients

	// make sure the encryptor can decrypt later
	recipients = s.ensureOurKeyID(ctx, recipients)

	ciphertext, err := s.crypto.Encrypt(ctx, sec.Bytes(), recipients)
	if err != nil {
		debug.Log("Failed encrypt secret: %s", err)
		return store.ErrEncrypt
	}

	if err := s.storage.Set(ctx, p, ciphertext); err != nil {
		return errors.Wrapf(err, "failed to write secret")
	}

	// It is not possible to perform concurrent git add and git commit commands
	// so we need to skip this step when using concurrency and perform them
	// at the end of the batch processing.
	if IsNoGitOps(ctx) {
		debug.Log("sub.Set(%s) - skipping git ops (disabled)")
		return nil
	}

	if err := s.rcs.Add(ctx, p); err != nil {
		if errors.Cause(err) == store.ErrGitNotInit {
			return nil
		}
		return errors.Wrapf(err, "failed to add '%s' to git", p)
	}

	if !ctxutil.IsGitCommit(ctx) {
		return nil
	}

	//queue.Add(func() error {
	//	return s.gitCommitAndPush(ctx, name)
	//})
	//return nil
	return s.gitCommitAndPush(ctx, name)
}

func (s *Store) gitCommitAndPush(ctx context.Context, name string) error {
	debug.Log("syncing with remote ...")
	if err := s.rcs.Commit(ctx, fmt.Sprintf("Save secret to %s: %s", name, ctxutil.GetCommitMessage(ctx))); err != nil {
		switch errors.Cause(err) {
		case store.ErrGitNotInit:
			debug.Log("commitAndPush - skipping git commit - git not initialized")
		case store.ErrGitNothingToCommit:
			debug.Log("commitAndPush - skipping git commit - nothing to commit")
		default:
			return errors.Wrapf(err, "failed to commit changes to git")
		}
	}

	if err := s.rcs.Push(ctx, "", ""); err != nil {
		if errors.Cause(err) == store.ErrGitNotInit {
			msg := "Warning: git is not initialized for this.storage. Ignoring auto-push option\n" +
				"Run: gopass git init"
			out.Error(ctx, msg)
			return nil
		}
		if errors.Cause(err) == store.ErrGitNoRemote {
			msg := "Warning: git has no remote. Ignoring auto-push option\n" +
				"Run: gopass git remote add origin ..."
			out.Yellow(ctx, msg)
			return nil
		}
		return errors.Wrapf(err, "failed to push to git remote")
	}
	debug.Log("synced with remote")
	//out.Green(ctx, "Pushed changes to remote")
	return nil
}
