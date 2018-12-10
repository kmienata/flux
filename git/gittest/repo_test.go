package gittest

import (
	"io/ioutil"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"context"

	"github.com/weaveworks/flux/cluster/kubernetes/testfiles"
	"github.com/weaveworks/flux/git"
	"github.com/weaveworks/flux/gpg/gpgtest"
)

type Note struct {
	Comment string
}

func TestCommit(t *testing.T) {
	config := TestConfig
	config.SkipMessage = " **SKIP**"
	checkout, repo, cleanup := CheckoutWithConfig(t, config)
	defer cleanup()

	for file, _ := range testfiles.Files {
		dirs := checkout.ManifestDirs()
		path := filepath.Join(dirs[0], file)
		if err := ioutil.WriteFile(path, []byte("FIRST CHANGE"), 0666); err != nil {
			t.Fatal(err)
		}
		break
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	commitAction := git.CommitAction{Message: "Changed file"}
	if err := checkout.CommitAndPush(ctx, commitAction, nil); err != nil {
		t.Fatal(err)
	}

	err := repo.Refresh(ctx)
	if err != nil {
		t.Error(err)
	}

	commits, err := repo.CommitsBefore(ctx, "HEAD")

	if err != nil {
		t.Fatal(err)
	}
	if len(commits) < 1 {
		t.Fatal("expected at least one commit")
	}
	if msg := commits[0].Message; msg != commitAction.Message+config.SkipMessage {
		t.Errorf(`expected commit message to be:

%s

    but it was

%s
`, commitAction.Message+config.SkipMessage, msg)
	}
}

func TestSignedCommit(t *testing.T) {
	gpgHome, signingKey, gpgCleanup := gpgtest.GPGKey(t)
	defer gpgCleanup()

	config := TestConfig
	checkout, repo, cleanup := CheckoutWithConfig(t, config)
	defer cleanup()

	for file, _ := range testfiles.Files {
		dirs := checkout.ManifestDirs()
		path := filepath.Join(dirs[0], file)
		if err := ioutil.WriteFile(path, []byte("FIRST CHANGE"), 0666); err != nil {
			t.Fatal(err)
		}
		break
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	commitAction := git.CommitAction{Message: "Changed file", SigningKey: signingKey, GPGHomeDir: gpgHome}
	if err := checkout.CommitAndPush(ctx, commitAction, nil); err != nil {
		t.Fatal(err)
	}

	err := repo.Refresh(ctx)
	if err != nil {
		t.Error(err)
	}

	commits, err := repo.CommitsBefore(ctx, "HEAD")

	if err != nil {
		t.Fatal(err)
	}
	if len(commits) < 1 {
		t.Fatal("expected at least one commit")
	}
	expectedKey := signingKey[len(signingKey)-16:]
	foundKey := commits[0].SigningKey[len(commits[0].SigningKey)-16:]
	if expectedKey != foundKey {
		t.Errorf(`expected commit signing key to be:
%s

    but it was

%s
`, expectedKey, foundKey)
	}
}

func TestCheckout(t *testing.T) {
	repo, cleanup := Repo(t)
	defer cleanup()

	sd, sg := make(chan struct{}), &sync.WaitGroup{}

	if err := repo.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	params := git.Config{
		Branch:    "master",
		UserName:  "example",
		UserEmail: "example@example.com",
		SyncTag:   "flux-test",
		NotesRef:  "fluxtest",
	}
	checkout, err := repo.Clone(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	defer checkout.Clean()

	// We don't expect any notes in the clone, yet. Make sure we get
	// no note, rather than an error.
	head, err := checkout.HeadRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var note Note
	ok, err := checkout.GetNote(ctx, head, &note)
	if err != nil {
		t.Error(err)
	}
	if ok {
		t.Errorf("Expected no note on head revision; got %#v", note)
	}

	changedFile := ""
	dirs := checkout.ManifestDirs()
	for file, _ := range testfiles.Files {
		path := filepath.Join(dirs[0], file)
		if err := ioutil.WriteFile(path, []byte("FIRST CHANGE"), 0666); err != nil {
			t.Fatal(err)
		}
		changedFile = file
		break
	}
	commitAction := git.CommitAction{Author: "", Message: "Changed file"}
	if err := checkout.CommitAndPush(ctx, commitAction, nil); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dirs[0], changedFile)
	if err := ioutil.WriteFile(path, []byte("SECOND CHANGE"), 0666); err != nil {
		t.Fatal(err)
	}
	// An example note with some of the fields filled in, so we can test
	// serialization a bit.
	expectedNote := Note{
		Comment: "Expected comment",
	}
	commitAction = git.CommitAction{Author: "", Message: "Changed file again"}
	if err := checkout.CommitAndPush(ctx, commitAction, &expectedNote); err != nil {
		t.Fatal(err)
	}

	check := func(c *git.Checkout) {
		contents, err := ioutil.ReadFile(filepath.Join(dirs[0], changedFile))
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != "SECOND CHANGE" {
			t.Error("contents in checkout are not what we committed")
		}
		rev, err := c.HeadRevision(ctx)
		if err != nil {
			t.Fatal(err)
		}

		var note Note
		ok, err := c.GetNote(ctx, rev, &note)
		if !ok {
			t.Error("note not found")
		}
		if err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(note, expectedNote) {
			t.Errorf("note is not what we supplied when committing: %#v", note)
		}
	}

	// Do we see the changes if we make another working checkout?
	if err := repo.Refresh(ctx); err != nil {
		t.Error(err)
	}

	another, err := repo.Clone(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	defer another.Clean()
	check(another)

	close(sd)
	sg.Wait()
}
