package ui

// Nerd Font glyphs, one cell wide each. They are escapes by code point because
// editors silently drop the private-use characters.
const (
	iconAgent   = "\U000F167A" // nf-md-robot_outline
	iconShell   = "\uF489"     // nf-oct-terminal
	iconZoom    = "\U000F0293" // nf-md-fullscreen
	iconScroll  = "\U000F005D" // nf-md-arrow_up
	iconLeader  = "\U000F030C" // nf-md-keyboard
	iconAlert   = "\U000F002A" // nf-md-alert_outline
	iconBlocked = "\U000F0026" // nf-md-alert
	iconIdle    = "\U000F05E1" // nf-md-check_circle_outline
	iconRename  = "\U000F0455" // nf-md-rename_box
	iconClock   = "\U000F0954" // nf-md-clock_outline
	iconGit     = "\uE725"     // nf-dev-git_branch
	iconLoad    = "\U000F0EE0" // nf-md-cpu_64_bit
	iconMem     = "\U000F035B" // nf-md-memory
	iconUsage   = "\U000F04C5" // nf-md-speedometer

	iconPanes     = "\U000F0BCC" // nf-md-view_split_vertical
	iconSplitDown = "\U000F0BCB" // nf-md-view_split_horizontal
	iconTabs      = "\U000F04E9" // nf-md-tab
	iconTabNew    = "\U000F075C" // nf-md-tab_plus
	iconCog       = "\U000F0493" // nf-md-cog
	iconPalette   = "\uF4B5"     // nf-oct-command_palette
	iconTheme     = "\U000F03D8" // nf-md-palette
	iconFocus     = "\U000F04FE" // nf-md-target
	iconResize    = "\U000F0616" // nf-md-arrow_expand
	iconSwap      = "\U000F04E1" // nf-md-swap_horizontal
	iconClose     = "\U000F0156" // nf-md-close
	iconQuit      = "\U000F0206" // nf-md-exit_to_app
	iconCopy      = "\U000F018F" // nf-md-content_copy

	iconMove            = "\U000F0DAD" // nf-md-application_export
	iconBreak           = "\U000F03CC" // nf-md-open_in_new
	iconLayoutBSP       = "\U000F0574" // nf-md-view_quilt
	iconLayoutMaster    = "\U000F06FD" // nf-md-page_layout_sidebar_left
	iconLayoutScrolling = "\U000F056D" // nf-md-view_column
	iconArrowLeft       = "\U000F004D" // nf-md-arrow_left
	iconArrowDown       = "\U000F0045" // nf-md-arrow_down
	iconArrowUp         = "\U000F005D" // nf-md-arrow_up
	iconArrowRight      = "\U000F0054" // nf-md-arrow_right
	iconRotate          = "\U000F0467" // nf-md-rotate_right
	iconEqualize        = "\U000F01FC" // nf-md-equal
	iconPromote         = "\U000F01A5" // nf-md-crown

	iconReview     = "\uF4D2"     // nf-oct-file_diff
	iconBranchMode = "\U000F062C" // nf-md-source_branch
	iconTurn       = "\U000F02DA" // nf-md-history
	iconSplit      = "\U000F08DF" // nf-md-format_columns
	iconFull       = "\U000F09EE" // nf-md-file_document_outline
	iconGhost      = "\U000F165D" // nf-md-ghost_outline
	iconFolder     = "\U000F0256" // nf-md-folder_outline
	iconMarkdown   = "\uF48A"     // nf-oct-markdown
	iconLinks      = "\U000F0339" // nf-md-link_variant
	iconPlan       = "\uF405"     // nf-oct-book
	iconLog        = "\uF4ED"     // nf-oct-log

	iconConversation = "\uF442" // nf-oct-comment_discussion

	iconNewWorktree = "\U000F14CA" // nf-md-source_branch_plus
	iconPin         = "\U000F0403" // nf-md-pin
	iconFollow      = "\U000F01A4" // nf-md-crosshairs_gps
	iconWorktrees   = "\U000F0CD0" // nf-md-source_repository_multiple

	iconPR       = "\uF407"     // nf-oct-git_pull_request
	iconPRDraft  = "\uF4DD"     // nf-oct-git_pull_request_draft
	iconPRClosed = "\uF4DC"     // nf-oct-git_pull_request_closed
	iconMerge    = "\uF419"     // nf-oct-git_merge
	iconPassed   = "\uF42E"     // nf-oct-check
	iconFailed   = "\uF467"     // nf-oct-x
	iconRunning  = "\U000F051F" // nf-md-timer_sand
	iconApproved = "\U000F0513" // nf-md-thumb_up
	iconChanges  = "\U000F03EB" // nf-md-pencil
	iconPush     = "\uF403"     // nf-oct-repo_push
	iconCleanup  = "\U000F00E2" // nf-md-broom
)

// Powerline edges and dividers.
const (
	edgeSolid = "\uE0BC" // nf-ple-upper_left_triangle
	edgeThin  = "\uE0BB" // nf-ple-forwardslash_separator
	trayOpen  = "\uE0BA" // nf-ple-lower_right_triangle
)

// spinFrames are the working badge's braille spinner frames, one cell wide each.
const spinFrames = "\u280B\u2819\u2839\u2838\u283C\u2834\u2826\u2827\u2807\u280F"

// spinFrame is the working badge's spinner glyph for frame n.
func spinFrame(n int) string {
	const w = 3 // every frame is one three-byte rune

	i := n % (len(spinFrames) / w)

	return spinFrames[i*w : (i+1)*w]
}
