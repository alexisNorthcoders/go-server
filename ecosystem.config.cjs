module.exports = {
  apps: [{
    name: "go-server",
    script: "./go-server",
    cwd: __dirname,
    env_production: {
      DATABASE_SECRET: process.env.DATABASE_SECRET,
    },
  }],
};
