package model

// PriorityNormal is the priority a ticket starts with when nobody says
// otherwise, by the name the migration seeds it under. It is marked is_system,
// so it can be neither renamed nor deleted, and the name goes on finding it.
const PriorityNormal = "normal"
