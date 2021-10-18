FROM postgres

RUN apt-get update
RUN apt-get install --yes vim ssh postgresql-14-wal2json

CMD ["postgres"]
