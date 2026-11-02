use std::fmt::Write as _;
use std::time::Instant;

#[must_use = "hold the Stage until the phase ends, then call `.done(..)`"]
pub struct Stage {
    name: &'static str,
    started_at: Instant,
}

impl Stage {
    pub fn start(name: &'static str) -> Self {
        eprintln!("stage start stage={name}");
        Self {
            name,
            started_at: Instant::now(),
        }
    }

    pub fn done(self, fields: &[(&str, &dyn std::fmt::Display)]) {
        let mut line = format!("stage done stage={}", self.name);
        for (key, value) in fields {
            let _ = write!(line, " {key}={value}");
        }
        let _ = write!(
            line,
            " duration={:.3}s",
            self.started_at.elapsed().as_secs_f64()
        );
        eprintln!("{line}");
    }
}
