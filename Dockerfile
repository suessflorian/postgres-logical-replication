FROM postgres

RUN apt-get update
RUN apt-get install --yes vim

CMD ["postgres"]
