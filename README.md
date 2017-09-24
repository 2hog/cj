# `cj` - Compose file powered jobs for Docker Swarm

`cj` allows you to run one-time jobs in a Swarm cluster, borrowing the spec from services you have already defined in a Docker Compose file.

## Why `cj`

There are many times that you need to execute jobs in a Swarm cluster, using the exact same configuration you have already defined for one of your services. This might include static asset compilation, migrations, or other tasks commonly needed during continuous integration and delivery.

`cj` provides a way to run jobs in a Swarm cluster, using one-off services and spec configuration using the Docker Compose format.

### Use cases

* Run database migrations before deploying a new version your application
* Collect static assets in a directory shared with your static web server (ie NGINX) for serving the new version of your application

## Usage

Less is more, that's why `cj` contains the least possible amount of commands to be usable, while being open to adding more when it makes sense. All you ever use `cj` for should be something like:

```bash
cj run --stack=mystack --service=django-web-service python manage.py migrate
```

## Inspiration

`cj` is highly inspired by [alexellis/jaas](https://github.com/alexellis/jaas) and the [Docker CLI](https://github.com/docker/cli)
