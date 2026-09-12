package dynamo

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/tinygodriver/nosql/dynamodb"
)

func sampleCredential(id, account string) auth.Credential {
	return auth.Credential{
		CredentialID:   []byte(id),
		AccountID:      account,
		UserHandle:     []byte("handle-" + account),
		PublicKey:      []byte("cose-blob"),
		PublicKeyX:     []byte("x"),
		PublicKeyY:     []byte("y"),
		Algorithm:      -7,
		SignCount:      1,
		BackupEligible: true,
		BackupState:    false,
		Transports:     []string{"internal", "hybrid"},
		Label:          "laptop",
		CreatedAt:      time.Now().Truncate(time.Second),
	}
}

func sampleBootstrap(loginID, account string) auth.BootstrapCredential {
	now := time.Now().Truncate(time.Second)
	return auth.BootstrapCredential{
		LoginID:           loginID,
		AccountID:         account,
		SecretDigest:      []byte("digest"),
		Purpose:           auth.PurposeInitialPasskey,
		IssuedAt:          now,
		ExpiresAt:         now.Add(time.Hour),
		AttemptsRemaining: 3,
	}
}

// --- allowlist ---------------------------------------------------------------

func TestAllowlistAnswersOneLoginInOneRequest(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	fake.table(DeclaredAllowlistTable)["S:"+allowlistEntry(
		"https://issuer.example", "email", "known@example.com")] =
		Entry("https://issuer.example", "email", "known@example.com", "operator")

	store := NewAllowlist()
	admitted, err := store.Registered(ctx, "https://issuer.example", []auth.AllowlistCandidate{
		{Claim: "sub", Value: "subject-1"},
		{Claim: "email", Value: "known@example.com"},
	})
	if err != nil || !admitted {
		t.Fatalf("registered = %v err = %v", admitted, err)
	}
	// Two compared claims, one round trip: that is what the batch signature is
	// for.
	if fake.calls["BatchGetItem"] != 1 {
		t.Fatalf("one login issued %d batch reads", fake.calls["BatchGetItem"])
	}
	if fake.calls["GetItem"] != 0 {
		t.Fatalf("the allowlist issued %d single reads", fake.calls["GetItem"])
	}
}

func TestAllowlistDeniesAnUnregisteredIdentity(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)

	admitted, err := NewAllowlist().Registered(ctx, "https://issuer.example",
		[]auth.AllowlistCandidate{{Claim: "sub", Value: "subject-1"}})
	if err != nil || admitted {
		t.Fatalf("unregistered = %v err = %v", admitted, err)
	}
}

// An entry registered for one issuer must not admit the same claim value under
// another, which is what joining the issuer into the key is for.
func TestAllowlistIsScopedToTheIssuer(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	fake.table(DeclaredAllowlistTable)["S:"+allowlistEntry(
		"https://issuer.example", "email", "known@example.com")] =
		Entry("https://issuer.example", "email", "known@example.com", "")

	admitted, err := NewAllowlist().Registered(ctx, "https://other.example",
		[]auth.AllowlistCandidate{{Claim: "email", Value: "known@example.com"}})
	if err != nil || admitted {
		t.Fatalf("cross-issuer match = %v err = %v", admitted, err)
	}
}

// An incomplete answer is not a non-match: the unread key might be the one that
// would have admitted this login.
func TestAllowlistUnreadKeysAreAnErrorNotADenial(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	fake.unread = 1

	_, err := NewAllowlist().Registered(ctx, "https://issuer.example",
		[]auth.AllowlistCandidate{{Claim: "sub", Value: "subject-1"}})
	if err == nil || !strings.Contains(err.Error(), "unread") {
		t.Fatalf("unprocessed keys = %v", err)
	}
}

func TestAllowlistFailureIsNotADenial(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	fake.failWith = "ValidationException"

	admitted, err := NewAllowlist().Registered(ctx, "https://issuer.example",
		[]auth.AllowlistCandidate{{Claim: "sub", Value: "subject-1"}})
	if err == nil || admitted {
		t.Fatalf("backend failure = %v err = %v", admitted, err)
	}
	if !errors.Is(err, dynamodb.ErrValidation) {
		t.Fatalf("driver sentinel must survive the mapping, got %v", err)
	}
}

func TestAllowlistWithNoCandidatesAsksNothing(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)

	admitted, err := NewAllowlist().Registered(ctx, "https://issuer.example", nil)
	if err != nil || admitted {
		t.Fatalf("no candidates = %v err = %v", admitted, err)
	}
	if fake.calls["BatchGetItem"] != 0 {
		t.Fatal("an empty question reached the service")
	}
}

// --- credentials -------------------------------------------------------------

func TestCredentialRoundTrip(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})
	original := sampleCredential("cred-1", "account-1")

	if err := store.Save(ctx, original, nil); err != nil {
		t.Fatalf("save = %v", err)
	}
	found, err := store.Find(ctx, original.CredentialID)
	if err != nil {
		t.Fatalf("find = %v", err)
	}
	if string(found.CredentialID) != "cred-1" || found.AccountID != "account-1" {
		t.Fatalf("identity did not round trip: %#v", found)
	}
	if string(found.PublicKey) != "cose-blob" || string(found.PublicKeyX) != "x" {
		t.Fatalf("key material did not round trip: %#v", found)
	}
	if found.Algorithm != -7 || found.SignCount != 1 || !found.BackupEligible || found.BackupState {
		t.Fatalf("protocol fields did not round trip: %#v", found)
	}
	if strings.Join(found.Transports, ",") != "internal,hybrid" || found.Label != "laptop" {
		t.Fatalf("descriptive fields did not round trip: %#v", found)
	}
}

func TestFindOfAnUnknownCredential(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)

	if _, err := NewCredentials(CredentialOptions{}).Find(ctx, []byte("absent")); !errors.Is(err, auth.ErrUnknownCredential) {
		t.Fatalf("unknown credential = %v", err)
	}
}

func TestListByAccountReadsTheIndex(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})

	for _, id := range []string{"cred-1", "cred-2"} {
		if err := store.Save(ctx, sampleCredential(id, "account-1"), nil); err != nil {
			t.Fatalf("save %s = %v", id, err)
		}
	}
	if err := store.Save(ctx, sampleCredential("cred-3", "account-2"), nil); err != nil {
		t.Fatalf("save for another account = %v", err)
	}

	listed, err := store.ListByAccount(ctx, "account-1")
	if err != nil {
		t.Fatalf("list = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d credentials, want 2", len(listed))
	}
	for _, credential := range listed {
		if credential.AccountID != "account-1" {
			t.Fatalf("another account's credential was listed: %#v", credential)
		}
		// The three fields a listing is read for. The user handle is the one
		// with teeth: an enrollment that could not see it would mint a second
		// handle for an account that already has one.
		if len(credential.CredentialID) == 0 {
			t.Fatalf("listing carried no credential ID: %#v", credential)
		}
		if string(credential.UserHandle) != "handle-account-1" {
			t.Fatalf("listing carried user handle %q", credential.UserHandle)
		}
		if strings.Join(credential.Transports, ",") != "internal,hybrid" {
			t.Fatalf("listing carried transports %v", credential.Transports)
		}
	}
}

// A retried enrollment must not become a second credential.
func TestSaveIsAnInsert(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})
	credential := sampleCredential("cred-1", "account-1")

	if err := store.Save(ctx, credential, nil); err != nil {
		t.Fatalf("first save = %v", err)
	}
	if err := store.Save(ctx, credential, nil); err == nil {
		t.Fatal("a repeated save overwrote the credential")
	}
}

// A counter that does not advance is a replayed or cloned authenticator, so the
// store refuses it rather than leaving the caller to notice.
func TestUpdateOnAssertionRefusesACounterThatDoesNotAdvance(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})
	credential := sampleCredential("cred-1", "account-1")
	if err := store.Save(ctx, credential, nil); err != nil {
		t.Fatalf("save = %v", err)
	}

	used := time.Now().Truncate(time.Second)
	if err := store.UpdateOnAssertion(ctx, credential.CredentialID, 5, true, used); err != nil {
		t.Fatalf("advancing counter = %v", err)
	}
	if err := store.UpdateOnAssertion(ctx, credential.CredentialID, 5, true, used); !errors.Is(err, auth.ErrUnknownCredential) {
		t.Fatalf("replayed counter = %v", err)
	}
	if err := store.UpdateOnAssertion(ctx, credential.CredentialID, 3, true, used); !errors.Is(err, auth.ErrUnknownCredential) {
		t.Fatalf("counter moving backwards = %v", err)
	}

	found, err := store.Find(ctx, credential.CredentialID)
	if err != nil {
		t.Fatalf("find = %v", err)
	}
	if found.SignCount != 5 || !found.BackupState || found.LastUsedAt.IsZero() {
		t.Fatalf("accepted ceremony output did not persist: %#v", found)
	}
}

// An authenticator that keeps no counter reports zero every time, which must
// not be read as a replay.
func TestUpdateOnAssertionAcceptsAnAuthenticatorWithoutACounter(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})
	credential := sampleCredential("cred-1", "account-1")
	credential.SignCount = 0
	if err := store.Save(ctx, credential, nil); err != nil {
		t.Fatalf("save = %v", err)
	}

	for range 2 {
		if err := store.UpdateOnAssertion(ctx, credential.CredentialID, 0, false, time.Now()); err != nil {
			t.Fatalf("counterless assertion = %v", err)
		}
	}
}

func TestUpdateOnAssertionOfAnUnknownCredential(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)

	err := NewCredentials(CredentialOptions{}).UpdateOnAssertion(ctx, []byte("absent"), 1, false, time.Now())
	if !errors.Is(err, auth.ErrUnknownCredential) {
		t.Fatalf("unknown credential = %v", err)
	}
}

// One account must not delete another's credential by ID.
func TestDeleteIsScopedToTheAccount(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})
	credential := sampleCredential("cred-1", "account-1")
	if err := store.Save(ctx, credential, nil); err != nil {
		t.Fatalf("save = %v", err)
	}

	if err := store.Delete(ctx, "account-2", credential.CredentialID); !errors.Is(err, auth.ErrUnknownCredential) {
		t.Fatalf("cross-account delete = %v", err)
	}
	if _, err := store.Find(ctx, credential.CredentialID); err != nil {
		t.Fatalf("the credential was removed anyway = %v", err)
	}
	if err := store.Delete(ctx, "account-1", credential.CredentialID); err != nil {
		t.Fatalf("owner delete = %v", err)
	}
	if _, err := store.Find(ctx, credential.CredentialID); !errors.Is(err, auth.ErrUnknownCredential) {
		t.Fatalf("the credential survived its delete = %v", err)
	}
}

func TestOversizedLabelIsRefusedBeforeTheRequest(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	credential := sampleCredential("cred-1", "account-1")
	credential.Label = strings.Repeat("x", maxLabelBytes+1)

	if err := NewCredentials(CredentialOptions{}).Save(ctx, credential, nil); err == nil {
		t.Fatal("an oversized label was accepted")
	}
	if fake.calls["PutItem"] != 0 {
		t.Fatalf("an oversized record reached the service %d times", fake.calls["PutItem"])
	}
}

// --- first enrollment --------------------------------------------------------

// Spending the bootstrap credential first is what makes the sequence
// single-use, so the order is asserted rather than assumed.
func TestSaveFirstCredentialSpendsBeforeItWrites(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})
	bootstrap := NewBootstrap()
	if err := bootstrap.Issue(ctx, sampleBootstrap("login-1", "account-1")); err != nil {
		t.Fatalf("issue = %v", err)
	}

	var order []string
	spend := func(ctx context.Context) error {
		order = append(order, "spend")
		return bootstrap.Consume(ctx, "login-1", time.Now())
	}
	activate := func(context.Context) error {
		order = append(order, "activate")
		return nil
	}
	credential := sampleCredential("cred-1", "account-1")
	if err := store.SaveFirstCredential(ctx, credential, spend, activate); err != nil {
		t.Fatalf("first enrollment = %v", err)
	}
	if strings.Join(order, ",") != "spend,activate" {
		t.Fatalf("order = %v", order)
	}
	if _, err := store.Find(ctx, credential.CredentialID); err != nil {
		t.Fatalf("the credential was not stored = %v", err)
	}
	// The secret is spent, so a second redemption cannot enroll a second
	// authenticator.
	if err := bootstrap.Consume(ctx, "login-1", time.Now()); !errors.Is(err, auth.ErrUnknownBootstrap) {
		t.Fatalf("second redemption = %v", err)
	}
}

// A failure at step one must leave nothing behind: no credential can exist
// while the secret that authorized it is still spendable.
func TestFailedSpendWritesNoCredential(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})
	credential := sampleCredential("cred-1", "account-1")

	spend := func(context.Context) error { return auth.ErrUnknownBootstrap }
	activated := false
	activate := func(context.Context) error { activated = true; return nil }

	if err := store.SaveFirstCredential(ctx, credential, spend, activate); !errors.Is(err, auth.ErrUnknownBootstrap) {
		t.Fatalf("failed spend = %v", err)
	}
	if activated {
		t.Fatal("the account was activated after a failed spend")
	}
	if _, err := store.Find(ctx, credential.CredentialID); !errors.Is(err, auth.ErrUnknownCredential) {
		t.Fatal("a credential was stored after a failed spend")
	}
}

// The named partial state: the secret is spent and the credential is stored,
// but activation failed. The account stays provisional, which is why it cannot
// create a session, and an administrator resolves it.
func TestFailedActivationLeavesTheNamedPartialState(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewCredentials(CredentialOptions{})
	bootstrap := NewBootstrap()
	if err := bootstrap.Issue(ctx, sampleBootstrap("login-1", "account-1")); err != nil {
		t.Fatalf("issue = %v", err)
	}
	credential := sampleCredential("cred-1", "account-1")

	spend := func(ctx context.Context) error { return bootstrap.Consume(ctx, "login-1", time.Now()) }
	activate := func(context.Context) error { return errors.New("account service unreachable") }

	if err := store.SaveFirstCredential(ctx, credential, spend, activate); err == nil {
		t.Fatal("a failed activation was reported as success")
	}
	// Nothing is rolled back: the two writes that did happen stay done.
	if _, err := store.Find(ctx, credential.CredentialID); err != nil {
		t.Fatalf("the credential was rolled back = %v", err)
	}
	if err := bootstrap.Consume(ctx, "login-1", time.Now()); !errors.Is(err, auth.ErrUnknownBootstrap) {
		t.Fatalf("the bootstrap credential was un-spent = %v", err)
	}
}

// --- bootstrap ---------------------------------------------------------------

func TestBootstrapRoundTrip(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewBootstrap()
	original := sampleBootstrap("login-1", "account-1")

	if err := store.Issue(ctx, original); err != nil {
		t.Fatalf("issue = %v", err)
	}
	found, err := store.Find(ctx, "login-1")
	if err != nil {
		t.Fatalf("find = %v", err)
	}
	if found.AccountID != "account-1" || string(found.SecretDigest) != "digest" ||
		found.Purpose != auth.PurposeInitialPasskey || found.AttemptsRemaining != 3 {
		t.Fatalf("record did not round trip: %#v", found)
	}
	if !found.ExpiresAt.Equal(original.ExpiresAt.UTC()) {
		t.Fatalf("expiry = %v, want %v", found.ExpiresAt, original.ExpiresAt.UTC())
	}
}

func TestIssueRefusesALiveLoginID(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewBootstrap()

	if err := store.Issue(ctx, sampleBootstrap("login-1", "account-1")); err != nil {
		t.Fatalf("first issue = %v", err)
	}
	if err := store.Issue(ctx, sampleBootstrap("login-1", "account-2")); err == nil {
		t.Fatal("re-issuing a live login ID overwrote it")
	}
	found, err := store.Find(ctx, "login-1")
	if err != nil || found.AccountID != "account-1" {
		t.Fatalf("after a refused issue = %#v err = %v", found, err)
	}
}

// The contract's sharpest requirement: two parallel guesses cannot both spend
// the last attempt.
func TestParallelAttemptsCannotBothSpendTheLastOne(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewBootstrap()
	credential := sampleBootstrap("login-1", "account-1")
	credential.AttemptsRemaining = 1
	if err := store.Issue(ctx, credential); err != nil {
		t.Fatalf("issue = %v", err)
	}

	const racers = 8
	var wait sync.WaitGroup
	results := make([]error, racers)
	wait.Add(racers)
	for index := range racers {
		go func() {
			defer wait.Done()
			_, results[index] = store.RecordAttempt(ctx, "login-1")
		}()
	}
	wait.Wait()

	spent := 0
	for _, err := range results {
		switch {
		case err == nil:
			spent++
		case errors.Is(err, auth.ErrUnknownBootstrap):
		default:
			t.Fatalf("unexpected attempt error = %v", err)
		}
	}
	if spent != 1 {
		t.Fatalf("%d callers spent a budget of one", spent)
	}
}

func TestRecordAttemptReportsWhatIsLeft(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewBootstrap()
	if err := store.Issue(ctx, sampleBootstrap("login-1", "account-1")); err != nil {
		t.Fatalf("issue = %v", err)
	}

	for _, want := range []int{2, 1, 0} {
		remaining, err := store.RecordAttempt(ctx, "login-1")
		if err != nil {
			t.Fatalf("attempt = %v", err)
		}
		if remaining != want {
			t.Fatalf("remaining = %d, want %d", remaining, want)
		}
	}
	if _, err := store.RecordAttempt(ctx, "login-1"); !errors.Is(err, auth.ErrUnknownBootstrap) {
		t.Fatalf("exhausted budget = %v", err)
	}
}

// An exhausted budget, a consumed credential, and an unknown login ID all
// answer the same, so a caller cannot enumerate accounts.
func TestBootstrapFailuresAreIndistinguishable(t *testing.T) {
	fake := newFakeTables()
	ctx := newTestContext(t, fake)
	store := NewBootstrap()
	if err := store.Issue(ctx, sampleBootstrap("consumed", "account-1")); err != nil {
		t.Fatalf("issue = %v", err)
	}
	if err := store.Consume(ctx, "consumed", time.Now()); err != nil {
		t.Fatalf("consume = %v", err)
	}

	for _, loginID := range []string{"consumed", "never-issued"} {
		if _, err := store.Find(ctx, loginID); !errors.Is(err, auth.ErrUnknownBootstrap) {
			t.Fatalf("find %q = %v", loginID, err)
		}
		if _, err := store.RecordAttempt(ctx, loginID); !errors.Is(err, auth.ErrUnknownBootstrap) {
			t.Fatalf("attempt %q = %v", loginID, err)
		}
		if err := store.Consume(ctx, loginID, time.Now()); !errors.Is(err, auth.ErrUnknownBootstrap) {
			t.Fatalf("consume %q = %v", loginID, err)
		}
	}
}

// --- tables ------------------------------------------------------------------

func TestTableDefinitionsUseTheirKeyAttributes(t *testing.T) {
	allowlist := AllowlistTable("deployed_allowlist")
	if allowlist.PartitionKey.Name != allowlistKeyAttribute || allowlist.SortKey != nil {
		t.Fatalf("allowlist keys = %#v", allowlist)
	}

	credentials := CredentialTable("deployed_credentials")
	if credentials.PartitionKey.Name != credentialKeyAttribute ||
		credentials.PartitionKey.Type != dynamodb.TypeBinary {
		t.Fatalf("credential key = %#v", credentials.PartitionKey)
	}
	// ListByAccount is the opposite question from Find, and the driver has no
	// UpdateTable, so the index has to be here at creation or never.
	if len(credentials.GlobalIndexes) != 1 ||
		credentials.GlobalIndexes[0].PartitionKey.Name != accountIndexAttribute {
		t.Fatalf("account index = %#v", credentials.GlobalIndexes)
	}

	bootstrap := BootstrapTable("deployed_bootstrap")
	if bootstrap.PartitionKey.Name != bootstrapKeyAttribute || bootstrap.SortKey != nil {
		t.Fatalf("bootstrap keys = %#v", bootstrap)
	}
}

func TestOperationsWithoutAClientNameTheImport(t *testing.T) {
	ctx := context.Background()
	_, err := NewAllowlist().Registered(ctx, "https://issuer.example",
		[]auth.AllowlistCandidate{{Claim: "sub", Value: "subject-1"}})
	if err == nil || !strings.Contains(err.Error(), "database/dynamo") {
		t.Fatalf("allowlist without a client = %v", err)
	}
	if _, err := NewCredentials(CredentialOptions{}).Find(ctx, []byte("cred-1")); err == nil ||
		!strings.Contains(err.Error(), "database/dynamo") {
		t.Fatalf("credentials without a client = %v", err)
	}
	if _, err := NewBootstrap().Find(ctx, "login-1"); err == nil ||
		!strings.Contains(err.Error(), "database/dynamo") {
		t.Fatalf("bootstrap without a client = %v", err)
	}
}
