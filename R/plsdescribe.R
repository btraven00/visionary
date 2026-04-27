# TODO: rename binary to "visionary" and update .release_url() to point to the
# new repo/release asset names. Add server and token args to describe_image()
# (or read from VISIONARY_SERVER / VISIONARY_TOKEN env vars) and drop the
# direct GEMINI_API_KEY requirement from the caller.

.binary_name <- function() "plsdescribe"

.platform_suffix <- function() {
  os <- tolower(Sys.info()[["sysname"]])
  arch <- Sys.info()[["machine"]]

  goos <- if (os == "darwin") "darwin" else "linux"
  goarch <- if (arch %in% c("arm64", "aarch64")) "arm64" else "amd64"

  paste0(goos, "-", goarch)
}

.release_url <- function() {
  base <- "https://github.com/btraven00/plsdescribe/releases/download/nightly"
  paste0(base, "/", .binary_name(), "-", .platform_suffix())
}

#' Find or download the plsdescribe binary
#'
#' Checks PATH first, then a per-user cache directory. Downloads from
#' GitHub releases if not found.
#'
#' @return Path to the binary.
#' @export
ensure_binary <- function() {
  name <- .binary_name()

  sys_path <- Sys.which(name)
  if (sys_path != "") return(unname(sys_path))

  cache_dir <- tools::R_user_dir("plsdescribe", which = "cache")
  if (!dir.exists(cache_dir)) dir.create(cache_dir, recursive = TRUE)

  dest <- file.path(cache_dir, name)

  if (!file.exists(dest)) {
    url <- .release_url()
    if (interactive()) {
      ans <- readline(paste0(
        "plsdescribe binary not found. Download from:\n  ", url,
        "\nand install to:\n  ", dest,
        "\nProceed? [Y/n] "
      ))
      if (tolower(trimws(ans)) %in% c("n", "no")) {
        stop("Download cancelled. Install the binary manually and add it to PATH.",
             call. = FALSE)
      }
    } else {
      message("Downloading plsdescribe from ", url)
    }
    utils::download.file(url, dest, mode = "wb", quiet = TRUE)
    Sys.chmod(dest, "0755")
    message("Installed to ", dest)
  }

  dest
}

#' Describe a plot image file
#'
#' @param image_path Path to an image file (PNG, JPEG, etc.)
#' @param verbose    Logical. Use detailed bullet-point description.
#' @param question   Optional follow-up question about the image.
#' @param tts        Logical. Speak the description via Google Cloud TTS
#'                   instead of returning text. Keeps stdout silent so it
#'                   won't collide with a screen reader.
#' @param tts_rate   Numeric. TTS speaking rate (0.25–2.0, 0 = default).
#' @return Character vector of the description (invisibly when tts = TRUE).
#' @export
describe_image <- function(image_path, verbose = FALSE, question = NULL,
                           tts = FALSE, tts_rate = 0) {
  bin <- ensure_binary()

  args <- c("-f", image_path)
  if (verbose) args <- c(args, "-v")
  if (tts)     args <- c(args, "-tts")
  if (tts_rate > 0) args <- c(args, "-tts-rate", as.character(tts_rate))
  if (!is.null(question) && nzchar(question)) args <- c(args, "-q", question)

  tmp_out <- tempfile("plsdesc_", fileext = ".txt")
  on.exit(unlink(tmp_out), add = TRUE)
  args <- c(args, "-o", tmp_out)

  out <- system2(bin, args, stdout = TRUE, stderr = FALSE)

  if (tts) {
    return(invisible(out))
  }

  out
}

#' Describe a plot expression
#'
#' Renders a plot to a temporary PNG, then describes it.
#'
#' @param expr       A plot expression (base R) or a ggplot/lattice object.
#' @param verbose    Logical. Use detailed description.
#' @param question   Optional question about the plot.
#' @param tts        Logical. Speak instead of returning text.
#' @param tts_rate   Numeric. TTS speaking rate (0.25–2.0, 0 = default).
#' @param width      PNG width in pixels.
#' @param height     PNG height in pixels.
#' @return Character vector of the description.
#' @export
describe_plot <- function(expr, verbose = FALSE, question = NULL, tts = FALSE,
                          tts_rate = 0, width = 800, height = 600) {
  tmp_png <- tempfile("plot_", fileext = ".png")
  on.exit(unlink(tmp_png), add = TRUE)

  grDevices::png(filename = tmp_png, width = width, height = height)
  tryCatch({
    result <- force(expr)
    if (inherits(result, "ggplot") || inherits(result, "trellis")) {
      print(result)
    }
  }, finally = {
    grDevices::dev.off()
  })

  describe_image(tmp_png, verbose = verbose, question = question, tts = tts,
                 tts_rate = tts_rate)
}
