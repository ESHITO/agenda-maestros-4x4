package mailer

// AttendeeZoneForTest exposes attendeeZone to the external test that keeps it in step
// with webhook.AttendeeZone (the mailer cannot import webhook; a test package can).
var AttendeeZoneForTest = attendeeZone
