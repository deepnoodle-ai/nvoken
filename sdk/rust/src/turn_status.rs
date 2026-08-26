//! Turn lifecycle classification shared by facade and stream reducers.

use crate::models;

pub const ALL_TURN_STATUSES: [models::TurnStatus; 8] = [
    models::TurnStatus::Queued,
    models::TurnStatus::Running,
    models::TurnStatus::Waiting,
    models::TurnStatus::BudgetHold,
    models::TurnStatus::Completed,
    models::TurnStatus::Incomplete,
    models::TurnStatus::Failed,
    models::TurnStatus::Cancelled,
];

pub const TERMINAL_TURN_STATUSES: [models::TurnStatus; 4] = [
    models::TurnStatus::Completed,
    models::TurnStatus::Incomplete,
    models::TurnStatus::Failed,
    models::TurnStatus::Cancelled,
];

pub fn is_terminal_status(status: models::TurnStatus) -> bool {
    TERMINAL_TURN_STATUSES.contains(&status)
}

pub fn is_turn_over(change: &models::TurnChange) -> bool {
    change.terminal || is_terminal_status(change.status)
}
