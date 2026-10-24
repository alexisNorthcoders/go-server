module.exports = {
  apps: [{
    name: "go-server",
    script: "./go-server",
    cwd: __dirname,
    env_production: {
      DATABASE_SECRET: process.env.DATABASE_SECRET,
      BOT_RESULTS_SECRET: process.env.BOT_RESULTS_SECRET,
    },
  }, {
    name: "go-server-dev",
    script: `${process.env.HOME}/go/bin/air`,
    args: "-c .air.toml",
    cwd: __dirname,
    interpreter: "none",
    watch: false,
    env: {
      PATH: `/usr/local/go/bin:${process.env.HOME}/go/bin:${process.env.PATH}`,
      // The Pi's go-server: also serve the endpoints taken over from its old
      // Node webserver.
      PI_ENDPOINTS: "true",
      // reddit-bot's Decision command, run by the /claude-techniques/api/decisions endpoints.
      REDDIT_BOT_NODE: process.env.REDDIT_BOT_NODE,
      REDDIT_BOT_DIR: process.env.REDDIT_BOT_DIR,
    },
  }],
};
