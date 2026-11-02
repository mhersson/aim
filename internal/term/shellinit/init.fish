# AIM's shell integration for fish. fish runs this file through -C, after the
# user's conf.d and config.fish, and it puts AIM's claude first.
if test -n "$AIM_SHELL_DIR"
    set -l __aim_bin "$AIM_SHELL_DIR/bin"
    set -l __aim_path
    for __aim_dir in $PATH
        test "$__aim_dir" = "$__aim_bin"; or set -a __aim_path $__aim_dir
    end
    set -gx PATH $__aim_bin $__aim_path
    if not functions -q claude
        function claude
            if test -x "$AIM_SHELL_DIR/bin/claude"
                "$AIM_SHELL_DIR/bin/claude" $argv
            else
                command claude $argv
            end
        end
    end
end
