module.exports = {
  apps: [{
    name: "go-server",
    script: "./go-server",
    cwd: __dirname,
    env_production: {
      DATABASE_SECRET: process.env.DATABASE_SECRET,
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
    },
  }],
};
