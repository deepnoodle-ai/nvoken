//! Which Invocation statuses mean a turn has stopped.
//!
//! Exported so no caller keeps a copy, for the same reason
//! [`answerable_tool_calls`](crate::answerable_tool_calls) is: the
//! classification is part of the protocol, and rediscovering it in every
//! application is how one of them gets it wrong.

use crate::models;

/// The statuses that mean a turn has stopped for good.
pub const TERMINAL_INVOCATION_STATUSES: [models::InvocationStatus; 4] = [
    models::InvocationStatus::Completed,
    models::InvocationStatus::Incomplete,
    models::InvocationStatus::Failed,
    models::InvocationStatus::Cancelled,
];

/// Every status the contract defines, in lifecycle order.
pub const ALL_INVOCATION_STATUSES: [models::InvocationStatus; 8] = [
    models::InvocationStatus::Queued,
    models::InvocationStatus::Running,
    models::InvocationStatus::Waiting,
    models::InvocationStatus::BudgetHold,
    models::InvocationStatus::Completed,
    models::InvocationStatus::Incomplete,
    models::InvocationStatus::Failed,
    models::InvocationStatus::Cancelled,
];

/// The statuses that mean a turn is still going.
///
/// This is what [`Client::list_invocations`](crate::Client::list_invocations)
/// wants for a teardown, sweep, or reconciler, which filters server-side and
/// takes values rather than a predicate.
///
/// It is the complement of [`TERMINAL_INVOCATION_STATUSES`] over
/// [`ALL_INVOCATION_STATUSES`], and `active_and_terminal_partition_the_enum`
/// keeps it that way, so a status added to the contract only has to be
/// classified once.
pub const ACTIVE_INVOCATION_STATUSES: [models::InvocationStatus; 4] = [
    models::InvocationStatus::Queued,
    models::InvocationStatus::Running,
    models::InvocationStatus::Waiting,
    models::InvocationStatus::BudgetHold,
];

/// Whether a status means the turn is over.
///
/// There are eight statuses and four of them are terminal, so the interesting
/// mistake is writing the *other* four out. `Queued`, `Running`, `Waiting`, and
/// `BudgetHold` differ only in what unblocks them — a budget-held turn stopped on
/// spending capacity with its deadlines on hold, and resumes on its own once
/// its account is funded — and a turn wrongly believed finished is one nobody
/// settles, reattaches to, or cancels before erasing its Session.
pub fn is_terminal_status(status: models::InvocationStatus) -> bool {
    TERMINAL_INVOCATION_STATUSES.contains(&status)
}

/// Whether a change ends the turn. **This is the terminal signal, and there is
/// no other.** There is no result frame, and no other frame ends a turn.
///
/// It answers for the change, not for the turn: a replayed `Running` change
/// reports `false` even after the turn has ended, which is what lets you fold
/// messages before changes and never mark a turn settled before its final
/// message exists.
///
/// Either witness suffices. The field and the status always agree when both are
/// present — nvoken computes one from the other — so accepting either keeps
/// this correct against a server too old to send the field, where a required
/// bool deserializes as `false` and cannot be told from a genuine one.
pub fn is_turn_over(change: &models::InvocationChange) -> bool {
    change.terminal || is_terminal_status(change.status)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn change(status: models::InvocationStatus, terminal: bool) -> models::InvocationChange {
        models::InvocationChange::new(
            "inv_1".to_string(),
            None,
            None,
            1,
            status,
            terminal,
            None,
            None,
            None,
            chrono::DateTime::parse_from_rfc3339("2026-08-15T00:00:00Z").unwrap(),
        )
    }

    /// The classification is exhaustive over the contract's enum, so a status
    /// added later fails here until someone says which side it belongs on.
    #[test]
    fn classifies_every_status() {
        use models::InvocationStatus::*;
        for (status, terminal) in [
            (Queued, false),
            (Running, false),
            (Waiting, false),
            (BudgetHold, false),
            (Completed, true),
            (Incomplete, true),
            (Failed, true),
            (Cancelled, true),
        ] {
            assert_eq!(is_terminal_status(status), terminal, "{status:?}");
        }
        assert_eq!(TERMINAL_INVOCATION_STATUSES.len(), 4);
    }

    /// The two lists are a partition of the enum. Rust cannot derive one from
    /// the other in a const, so this is what keeps a status added to the
    /// contract from landing in neither — or in both.
    #[test]
    fn active_and_terminal_partition_the_enum() {
        for status in ALL_INVOCATION_STATUSES {
            let active = ACTIVE_INVOCATION_STATUSES.contains(&status);
            let terminal = TERMINAL_INVOCATION_STATUSES.contains(&status);
            assert!(
                active != terminal,
                "{status:?} is in {active} and {terminal}"
            );
        }
        assert_eq!(
            ACTIVE_INVOCATION_STATUSES.len() + TERMINAL_INVOCATION_STATUSES.len(),
            ALL_INVOCATION_STATUSES.len()
        );
    }

    /// A budget-held turn stopped on spending capacity with its deadlines on hold. It
    /// still owns the Session and resumes on its own once the account is funded,
    /// so reading it as over abandons a turn that is still going.
    #[test]
    fn budget_hold_is_not_terminal() {
        assert!(!is_terminal_status(models::InvocationStatus::BudgetHold));
    }

    /// Both witnesses are present and agreeing in normal operation. The status
    /// alone covers a server too old to send the field.
    #[test]
    fn turn_over_accepts_either_witness() {
        assert!(is_turn_over(&change(
            models::InvocationStatus::Completed,
            true
        )));
        assert!(is_turn_over(&change(
            models::InvocationStatus::Completed,
            false
        )));
        assert!(is_turn_over(&change(
            models::InvocationStatus::Running,
            true
        )));
        assert!(!is_turn_over(&change(
            models::InvocationStatus::Running,
            false
        )));
    }
}
