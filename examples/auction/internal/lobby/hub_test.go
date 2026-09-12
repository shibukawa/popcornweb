package lobby

import (
	"testing"
)

func TestNotifyRoomsChangedCoalescesBursts(t *testing.T) {
	changed, unsubscribe := Subscribe()
	defer unsubscribe()

	NotifyRoomsChanged()
	NotifyRoomsChanged()
	NotifyRoomsChanged()

	select {
	case <-changed:
	default:
		t.Fatal("subscriber did not receive the coalesced room change")
	}
	select {
	case <-changed:
		t.Fatal("burst produced more than one pending notification")
	default:
	}
}

func TestNotifyRoomChangedOnlyWakesMatchingRoom(t *testing.T) {
	roomOne, unsubscribeOne := SubscribeRoom(1)
	defer unsubscribeOne()
	roomTwo, unsubscribeTwo := SubscribeRoom(2)
	defer unsubscribeTwo()

	NotifyRoomChanged(1)
	select {
	case <-roomOne:
	default:
		t.Fatal("room one listener was not notified")
	}
	select {
	case <-roomTwo:
		t.Fatal("room two listener was notified")
	default:
	}
}
