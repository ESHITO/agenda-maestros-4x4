package handler

// Fork (Agenda Maestros 4x4): a replaced photo must show everywhere at once.
//
// ServeAvatar answers GET /avatars/{id} with `Cache-Control: private, max-age=86400`, and
// upstream stored the same "/avatars/{id}" after every upload, so a browser that had
// already loaded the old photo (the admin's Members page, the sidebar, a visitor's booking
// page) kept showing it for up to a day after a replacement - the admin reads that as
// "the change did not save". storeAvatar now stores a URL that changes with each upload:
// "/avatars/{id}?v={unix ms}". The mux matches on the path and ServeAvatar never reads the
// query, so the file is served as before; every surface reads avatar_url from the users
// row and so asks for the new file. Rows written before this change keep the bare URL,
// which still works.

import (
	"strconv"
	"time"
)

// versionedAvatarURL is the avatar_url stored for a photo uploaded at `at`.
func versionedAvatarURL(userID string, at time.Time) string {
	return "/avatars/" + userID + "?v=" + strconv.FormatInt(at.UnixMilli(), 10)
}
