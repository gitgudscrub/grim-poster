"""Approved Sharktower copy for *unscheduled* next-game announcements.

Pure helper for the private Events notifier. This module intentionally does not
post to Discord or read any bot credentials. Only call for confirmed no-event
requests after explicit Storyteller confirmation.
"""

UNSCHEDULED_NEXT_GAME_TEXT = (
    "🦈 **Another game of Blood on the Clocktower?**\n\n"
    "The next game is being organised!\n\n"
    "If you're around and fancy joining another game, come along!"
)


def unscheduled_next_game_payload() -> dict:
    """Return Discord REST/webhook JSON with *all* mentions disabled."""
    return {
        "content": UNSCHEDULED_NEXT_GAME_TEXT,
        "allowed_mentions": {"parse": []},
    }
